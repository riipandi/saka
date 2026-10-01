package signin

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/pkg/responder"

	authnv1 "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1"
	authnv1connect "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1/authnv1connect"
	"github.com/riipandi/tango/modules/identity/jwks"
	"google.golang.org/protobuf/types/known/timestamppb"
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

// MountRPC registers the procedures on the RPC router. The handler options
// are the transport's — the shared snake_case codec and the panic boundary —
// so the procedure answers exactly like the transport's own.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := authnv1connect.NewAuthServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(authnv1connect.AuthServiceSignInProcedure, handler)
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
func (h *rpcHandler) SignIn(ctx context.Context, req *connect.Request[authnv1.SignInRequest]) (*connect.Response[authnv1.SignInResponse], error) {
	body := req.Msg
	if body.Identity == "" || body.Password == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("identity and password are required"))
	}

	// The client facts are the transport's: one middleware captured them
	// from the request before the procedure ran, so this handler reads the
	// same address, agent, and fingerprint the audit record carries rather
	// than re-deriving a narrower set from the connect request.
	client := audit.ClientFromContext(ctx)

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

	// The MFA fork changes the response's shape, not its envelope: the
	// challenge answer carries no token fields, and the message names the
	// next procedure rather than pretending a session opened.
	message := "the token pair was issued"
	if result.MFARequired {
		message = "the second factor is required to complete the sign-in"
	}
	return connect.NewResponse(&authnv1.SignInResponse{
		AccessToken:      result.AccessToken,
		TokenType:        result.TokenType,
		AccessExpiresIn:  result.AccessExpiresIn,
		RefreshExpiresIn: result.RefreshExpiresIn,
		RefreshToken:     result.RefreshToken,
		SessionId:        result.SessionID,
		MfaRequired:      result.MFARequired,
		MfaPendingToken:  result.MFAPendingToken,
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

		Status:  responder.StatusSuccess,
		Message: message,
	}), nil
}

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose text
// names nothing a caller could aim at.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	case errors.Is(err, ErrAccountDisabled):
		return connect.NewError(connect.CodePermissionDenied, errors.New("account is disabled"))
	case errors.Is(err, ErrAccountBanned):
		return connect.NewError(connect.CodePermissionDenied, errors.New("account is banned"))
	case errors.Is(err, ErrEmailUnverified):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("email address is not verified"))
	case errors.Is(err, jwks.ErrNoSigningKey):
		return connect.NewError(connect.CodeInternal, errors.New("sign-in is not answerable"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("sign-in failed"))
	}
}
