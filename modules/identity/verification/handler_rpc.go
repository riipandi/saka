package verification

import (
	"context"
	"errors"
	"uuid"

	"github.com/riipandi/tango/pkg/responder"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	identityv1 "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1"
	identityv1connect "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1/identityv1connect"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/jwtutils"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "verification"

// Module serves the email-verification procedures. Everything it answers is
// an RPC procedure, so its HTTP mount is empty by construction.
type Module struct {
	service *Service
}

// NewModule builds the module over the verification service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the endpoints on the HTTP router. The feature serves no
// plain HTTP route: the token travels through the frontend, so a procedure
// is POST-only on the RPC surface.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC router. The handler options
// are the transport's — the shared snake_case codec and the panic boundary —
// so the procedures answer exactly like the transport's own. Each procedure
// is registered at its own path: the generated handler answers a path under
// its prefix it does not know with a plain-text 404, which a Connect client
// cannot read.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := identityv1connect.NewEmailVerificationServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(identityv1connect.EmailVerificationServiceSendEmailProcedure, handler)
	r.Handle(identityv1connect.EmailVerificationServiceVerifyEmailProcedure, handler)
	r.Handle(identityv1connect.EmailVerificationServiceRequestEmailChangeProcedure, handler)
	r.Handle(identityv1connect.EmailVerificationServiceConfirmEmailChangeProcedure, handler)
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) identityv1connect.EmailVerificationServiceHandler {
	return &rpcHandler{service: service}
}

// SendEmail mails the verification code to the signed-in account's address.
// The procedure is the signed-in user's own door, not an administrative one:
// any authenticated caller may ask for their own message, and the claims —
// not the request — name the account.
func (h *rpcHandler) SendEmail(ctx context.Context, req *connect.Request[identityv1.SendVerificationEmailRequest]) (*connect.Response[identityv1.SendVerificationEmailResponse], error) {
	if _, ok := jwtutils.CallerFrom(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	if err := h.service.SendEmail(ctx, callerIDOf(ctx)); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.SendVerificationEmailResponse{
		Status:  responder.StatusSuccess,
		Message: "the verification email was sent",
	}), nil
}

// VerifyEmail marks the token's account as verified. The procedure is
// public: the token is the credential, and the caller carries none — the
// message typed in from a screen that may hold no session.
func (h *rpcHandler) VerifyEmail(ctx context.Context, req *connect.Request[identityv1.VerifyEmailRequest]) (*connect.Response[identityv1.VerifyEmailResponse], error) {
	if err := h.service.VerifyEmail(ctx, req.Msg.Token); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.VerifyEmailResponse{
		Status:  responder.StatusSuccess,
		Message: "the email address was verified",
	}), nil
}

// callerIDOf reads the account the access token names — the wire-form
// TypeID the claims carry, converted the user package's one way. The guard
// has already admitted the caller, so an absent identity is the internal
// state a wiring bug produces.
func callerIDOf(ctx context.Context) uuid.UUID {
	caller, _ := jwtutils.CallerFrom(ctx)
	id, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return uuid.UUID{}
	}
	return id
}

// RequestEmailChange writes the signed-in account's pending change and mails
// its token to the address the change moves to. The procedure is the
// signed-in user's own door: the claims — not the request — name the
// account, so a caller can only ever start a change for their own address.
func (h *rpcHandler) RequestEmailChange(ctx context.Context, req *connect.Request[identityv1.RequestEmailChangeRequest]) (*connect.Response[identityv1.RequestEmailChangeResponse], error) {
	if _, ok := jwtutils.CallerFrom(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	if err := h.service.RequestEmailChange(ctx, callerIDOf(ctx), req.Msg.NewEmail); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.RequestEmailChangeResponse{
		Status:  responder.StatusSuccess,
		Message: "the change was requested; the confirm code was sent to the new address",
	}), nil
}

// ConfirmEmailChange consumes the pending token and moves the account to the
// address it binds. The procedure is public: the token is the credential,
// and the caller carries none — the message typed in from a screen that
// may hold no session.
func (h *rpcHandler) ConfirmEmailChange(ctx context.Context, req *connect.Request[identityv1.ConfirmEmailChangeRequest]) (*connect.Response[identityv1.ConfirmEmailChangeResponse], error) {
	if err := h.service.ConfirmEmailChange(ctx, req.Msg.Token); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.ConfirmEmailChangeResponse{
		Status:  responder.StatusSuccess,
		Message: "the email address was changed",
	}), nil
}

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose text
// names nothing a caller could aim at. A malformed field never reaches the
// service: the transport's validate interceptor refuses it with the typed
// violation details the contracts carry.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrUserNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("account not found"))
	case errors.Is(err, ErrEmailChangeDisabled):
		return connect.NewError(connect.CodeNotFound, errors.New("email change is not available"))
	case errors.Is(err, ErrAlreadyVerified):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("email already verified"))
	case errors.Is(err, ErrMailUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("mailer is not configured"))
	case errors.Is(err, ErrInvalidToken):
		return connect.NewError(connect.CodePermissionDenied, errors.New("verification token is invalid or expired"))
	case errors.Is(err, ErrResendTooSoon):
		return connect.NewError(connect.CodeResourceExhausted, errors.New("a verification email was sent less than a minute ago"))
	case errors.Is(err, ErrSameEmail):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the new address is the current one"))
	case errors.Is(err, ErrEmailTaken):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("the address is already in use"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("email verification failed"))
	}
}
