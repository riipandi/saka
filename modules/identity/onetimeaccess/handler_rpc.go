package onetimeaccess

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	"google.golang.org/protobuf/types/known/timestamppb"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/webutil"

	authnv1 "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1"
	authnv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1/authnv1connect"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "onetimeaccess"

// Module serves the one-time access procedures. Everything it answers is an
// RPC procedure, so its HTTP mount is empty by construction.
type Module struct {
	service *Service
}

// NewModule builds the module over the one-time access service.
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
	_, handler := authnv1connect.NewOneTimeAccessServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(authnv1connect.OneTimeAccessServiceCreateTokenProcedure, handler)
	r.Handle(authnv1connect.OneTimeAccessServiceExchangeTokenProcedure, handler)
	r.Handle(authnv1connect.OneTimeAccessServiceRequestEmailAsAdminProcedure, handler)
	r.Handle(authnv1connect.OneTimeAccessServiceRequestEmailProcedure, handler)
}

// rpcHandler is the transport mapping of the one-time access procedures. The
// service carries the rules; this type carries the connect codes and the
// request facts the protocol supplies on its own.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) authnv1connect.OneTimeAccessServiceHandler {
	return &rpcHandler{service: service}
}

// CreateToken issues a code for one account, for an administrator to hand
// over.
func (h *rpcHandler) CreateToken(ctx context.Context, req *connect.Request[authnv1.CreateOneTimeAccessTokenRequest]) (*connect.Response[authnv1.CreateOneTimeAccessTokenResponse], error) {
	body := req.Msg
	token, expiresAt, err := h.service.CreateToken(ctx, body.Id, body.GetTtlSeconds())
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.CreateOneTimeAccessTokenResponse{
		Token:     token,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Status:    webutil.StatusSuccess,
		Message:   "the one-time access code was created",
	}), nil
}

// ExchangeToken consumes a code and signs its holder in.
func (h *rpcHandler) ExchangeToken(ctx context.Context, req *connect.Request[authnv1.ExchangeOneTimeAccessTokenRequest]) (*connect.Response[authnv1.ExchangeOneTimeAccessTokenResponse], error) {
	body := req.Msg

	// The client facts are the transport's: one middleware captured them
	// from the request before the procedure ran, so the session the exchange
	// opens carries the same address, agent, and fingerprint the audit
	// record does.
	client := fwaudit.ClientFromContext(ctx)

	result, err := h.service.Exchange(ctx, body.Token, body.GetDeviceToken(), client)
	if err != nil {
		return nil, mapError(err)
	}
	// The MFA fork changes the response's shape, not its envelope: the
	// challenge answer carries no token fields, and the message names the
	// next procedure rather than pretending a session opened.
	message := "the one-time access code was exchanged for a session"
	if result.MFARequired {
		message = "the second factor is required to complete the sign-in"
	}
	return connect.NewResponse(&authnv1.ExchangeOneTimeAccessTokenResponse{
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
		Status:  webutil.StatusSuccess,
		Message: message,
	}), nil
}

// RequestEmailAsAdmin sends a code to one account's address.
func (h *rpcHandler) RequestEmailAsAdmin(ctx context.Context, req *connect.Request[authnv1.RequestOneTimeAccessEmailAsAdminRequest]) (*connect.Response[authnv1.RequestOneTimeAccessEmailAsAdminResponse], error) {
	body := req.Msg
	if err := h.service.RequestEmailAsAdmin(ctx, body.Id, body.GetTtlSeconds()); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.RequestOneTimeAccessEmailAsAdminResponse{
		Status:  webutil.StatusSuccess,
		Message: "the one-time access code was sent to the account's email address",
	}), nil
}

// RequestEmail sends a code to the address the caller names.
func (h *rpcHandler) RequestEmail(ctx context.Context, req *connect.Request[authnv1.RequestOneTimeAccessEmailRequest]) (*connect.Response[authnv1.RequestOneTimeAccessEmailResponse], error) {
	body := req.Msg
	deviceToken, err := h.service.RequestEmail(ctx, body.Email)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.RequestOneTimeAccessEmailResponse{
		DeviceToken: deviceToken,
		Status:      webutil.StatusSuccess,
		Message:     "if the address names an account, a code is on its way",
	}), nil
}

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose text
// names nothing a caller could aim at.
//
// The two authentication refusals are the deliberate pair: an unknown,
// expired, or spent code and a device token that does not match answer the
// same code but different messages, because a holder who lost the device
// token must know to re-request rather than retype, while neither message
// tells a caller which of the two halves of the pair was the lie.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrUserNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("account not found"))
	case errors.Is(err, ErrFeatureDisabled):
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("the one-time access email path is disabled"))
	case errors.Is(err, ErrMailUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("mailer is not configured"))
	case errors.Is(err, ErrQueueUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("queue is not configured"))
	case errors.Is(err, ErrDeviceMismatch):
		return connect.NewError(connect.CodeUnauthenticated,
			errors.New("the device token does not match"))
	case errors.Is(err, ErrTokenInvalid):
		return connect.NewError(connect.CodeUnauthenticated,
			errors.New("the one-time access code is invalid or expired"))
	case errors.Is(err, ErrResendTooSoon):
		return connect.NewError(connect.CodeResourceExhausted,
			errors.New("an access code email was sent less than a minute ago"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
}
