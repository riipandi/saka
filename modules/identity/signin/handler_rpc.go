package signin

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	"github.com/go-chi/chi/v5"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/webutil"

	"google.golang.org/protobuf/types/known/timestamppb"

	authnv1 "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1"
	authnv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1/authnv1connect"
	"github.com/riipandi/saka/modules/identity/jwks"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "signin"

// Module serves the sign-in procedures. Everything it answers is an RPC
// procedure, so its HTTP mount is empty by construction.
type Module struct {
	service *Service
}

// NewModule builds the module over the sign-in service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the endpoints on the HTTP router. The feature serves no
// plain HTTP route: a procedure is POST-only on the RPC surface.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC server. The server carries the
// transport's interceptors and the mount the shared snake_case codec, so the
// procedure answers exactly like the transport's own.
func (m *Module) MountRPC(server *connect.Server) {
	authnv1connect.RegisterAuthServiceHandler(server, newRPCHandler(m.service))
}

// rpcHandler is the transport mapping of the sign-in procedures. The service
// carries the rules; this type carries the connect codes and the request
// facts the protocol supplies on its own.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) authnv1connect.AuthServiceHandler {
	return &rpcHandler{service: service}
}

// SignIn verifies the credential and answers the token pair.
func (h *rpcHandler) SignIn(ctx context.Context, req *authnv1.SignInRequest) (*authnv1.SignInResponse, error) {
	body := req
	if body.Identity == "" || body.Password == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			"identity and password are required")
	}

	// The client facts are the transport's: one middleware captured them
	// from the request before the procedure ran, so this handler reads the
	// same address, agent, and fingerprint the audit record carries rather
	// than re-deriving a narrower set from the connect request.
	client := fwaudit.ClientFromContext(ctx)

	result, err := h.service.SignIn(ctx, Params{
		Identity:    body.Identity,
		Password:    body.Password,
		Remember:    body.GetRemember(),
		UserAgent:   client.UserAgent,
		IPAddress:   client.IPAddress,
		Fingerprint: client.Fingerprint,
	})
	if err != nil {
		return nil, mapError(err)
	}

	// The MFA forks change the response's shape, not its envelope: the
	// challenge and the enrollment answers carry no token fields, and the
	// message names the next procedure rather than pretending a session
	// opened.
	message := "the token pair was issued"
	switch {
	case result.MFAEnrollmentRequired:
		message = "a second factor must be enrolled before the sign-in completes"
	case result.MFARequired:
		message = "the second factor is required to complete the sign-in"
	}
	return &authnv1.SignInResponse{
		AccessToken:           result.AccessToken,
		TokenType:             result.TokenType,
		AccessExpiresIn:       result.AccessExpiresIn,
		RefreshExpiresIn:      result.RefreshExpiresIn,
		RefreshToken:          result.RefreshToken,
		SessionId:             result.SessionID,
		MfaRequired:           result.MFARequired,
		MfaEnrollmentRequired: result.MFAEnrollmentRequired,
		MfaPendingToken:       result.MFAPendingToken,
		MfaPendingExpiresAt: func() *timestamppb.Timestamp {
			if result.MFAPendingExpiresAt.IsZero() {
				return nil
			}
			return timestamppb.New(result.MFAPendingExpiresAt)
		}(),
		User: &authnv1.AuthenticatedUser{
			Id:          result.User.ID,
			Username:    result.User.Username,
			Email:       result.User.Email,
			DisplayName: result.User.DisplayName,
		},

		Status:  webutil.StatusSuccess,
		Message: message,
	}, nil
}

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose text
// names nothing a caller could aim at.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		return connect.NewError(connect.CodeUnauthenticated, "invalid credentials")
	case errors.Is(err, ErrAccountDisabled):
		return connect.NewError(connect.CodePermissionDenied, "account is disabled")
	case errors.Is(err, ErrAccountBanned):
		return connect.NewError(connect.CodePermissionDenied, "account is banned")
	case errors.Is(err, ErrSigninRestricted):
		return connect.NewError(connect.CodePermissionDenied, "sign-in is not permitted")
	case errors.Is(err, ErrEmailUnverified):
		return connect.NewError(connect.CodeFailedPrecondition, "email address is not verified")
	case errors.Is(err, jwks.ErrNoSigningKey):
		return connect.NewError(connect.CodeInternal, "sign-in is not answerable")
	default:
		return connect.NewError(connect.CodeInternal, "sign-in failed")
	}
}
