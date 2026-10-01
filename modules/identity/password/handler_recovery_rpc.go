package password

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	authnv1 "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1"
	authnv1connect "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1/authnv1connect"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/riipandi/tango/pkg/responder"
)

// RecoveryModuleName is the name this feature reports under. The area it
// belongs to qualifies it, so the name is the feature alone.
const RecoveryModuleName = "password-recovery"

// RecoveryModule serves the password-recovery procedures. Everything it
// answers is an RPC procedure, so its HTTP mount is empty by construction.
type RecoveryModule struct {
	service *Service
	// exposeResetToken mirrors the deployment's `app.expose_reset_token`:
	// when true, ForgotPassword answers the raw token in the body.
	exposeResetToken bool
}

// NewRecoveryModule builds the module over the recovery service.
func NewRecoveryModule(service *Service) *RecoveryModule {
	return &RecoveryModule{service: service}
}

// WithExposedResetToken sets whether ForgotPassword answers the raw token.
// The configuration decides it, not the request.
func (m *RecoveryModule) WithExposedResetToken(expose bool) *RecoveryModule {
	m.exposeResetToken = expose
	return m
}

// Name reports the module in composition reports.
func (m *RecoveryModule) Name() string { return RecoveryModuleName }

// Mount registers the endpoints on the HTTP router. The feature serves no
// plain HTTP route: the token travels through the frontend, so a procedure
// is POST-only on the RPC surface.
func (m *RecoveryModule) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC router. The handler options
// are the transport's — the shared snake_case codec and the panic boundary —
// so the procedures answer exactly like the transport's own.
func (m *RecoveryModule) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := authnv1connect.NewPasswordRecoveryServiceHandler(newRecoveryHandler(m.service, m.exposeResetToken), opts...)
	r.Handle(authnv1connect.PasswordRecoveryServiceForgotPasswordProcedure, handler)
	r.Handle(authnv1connect.PasswordRecoveryServiceResetPasswordProcedure, handler)
	r.Handle(authnv1connect.PasswordRecoveryServiceAdminResetUserPasswordProcedure, handler)
}

// recoveryHandler is the transport mapping of the procedures. The service
// carries the rules; this type carries the connect codes.
type recoveryHandler struct {
	service *Service
	// exposeResetToken mirrors the module's deployment decision: when true,
	// ForgotPassword answers the raw token in the body.
	exposeResetToken bool
}

// newRecoveryHandler builds the handler over the service.
func newRecoveryHandler(service *Service, exposeResetToken bool) authnv1connect.PasswordRecoveryServiceHandler {
	return &recoveryHandler{service: service, exposeResetToken: exposeResetToken}
}

// ForgotPassword issues the reset email for the named address. The procedure
// is public — a caller who lost the password holds no credential — and the
// answer is the same whether the account exists or not. The raw token rides
// the response only when the deployment exposes it.
func (h *recoveryHandler) ForgotPassword(ctx context.Context, req *connect.Request[authnv1.ForgotPasswordRequest]) (*connect.Response[authnv1.ForgotPasswordResponse], error) {
	raw, err := h.service.ForgotPassword(ctx, req.Msg.Email)
	if err != nil {
		return nil, mapRecoveryError(err)
	}
	resp := &authnv1.ForgotPasswordResponse{
		Status:  responder.StatusSuccess,
		Message: "if the address names an account, a reset email was sent",
	}
	if h.exposeResetToken {
		resp.ResetToken = raw
	}
	return connect.NewResponse(resp), nil
}

// ResetPassword spends the token on a new password. The procedure is public:
// the token is the credential, and the caller carries none — the message
// linked here from a browser that may hold no session. The sessions the
// account holds are revoked unless the request spares them.
func (h *recoveryHandler) ResetPassword(ctx context.Context, req *connect.Request[authnv1.ResetPasswordRequest]) (*connect.Response[authnv1.ResetPasswordResponse], error) {
	terminate := true
	if req.Msg.TerminateSessions != nil {
		terminate = *req.Msg.TerminateSessions
	}
	if err := h.service.ResetPassword(ctx, req.Msg.Token, req.Msg.NewPassword, terminate); err != nil {
		return nil, mapRecoveryError(err)
	}
	return connect.NewResponse(&authnv1.ResetPasswordResponse{
		Status:  responder.StatusSuccess,
		Message: "the password was reset",
	}), nil
}

// AdminResetUserPassword triggers the reset email on a named account. The
// caller is administrative by the guard's default; the service answers the
// account-level failures the impersonating caller is never allowed to see.
func (h *recoveryHandler) AdminResetUserPassword(ctx context.Context, req *connect.Request[authnv1.AdminResetUserPasswordRequest]) (*connect.Response[authnv1.AdminResetUserPasswordResponse], error) {
	if _, ok := jwtutils.CallerFrom(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	if err := h.service.AdminResetUserPassword(ctx, req.Msg.UserId); err != nil {
		return nil, mapRecoveryError(err)
	}
	return connect.NewResponse(&authnv1.AdminResetUserPasswordResponse{
		Status:  responder.StatusSuccess,
		Message: "a reset email was sent to the account's address",
	}), nil
}

// mapRecoveryError translates the service's failures into the codes the
// Connect protocol carries. The internal ones are collapsed to one answer
// whose text names nothing a caller could aim at.
func mapRecoveryError(err error) error {
	switch {
	case errors.Is(err, ErrUserNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("account not found"))
	case errors.Is(err, ErrNoPassword):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the account holds no password credential"))
	case errors.Is(err, ErrAccountForbidden):
		return connect.NewError(connect.CodePermissionDenied, errors.New("the account is disabled or banned"))
	case errors.Is(err, ErrMailUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("mailer is not configured"))
	case errors.Is(err, ErrInvalidToken):
		return connect.NewError(connect.CodePermissionDenied, errors.New("reset token is invalid or expired"))
	case errors.Is(err, ErrResendTooSoon):
		return connect.NewError(connect.CodeResourceExhausted, errors.New("a reset email was sent less than a minute ago"))
	case errors.Is(err, ErrSamePassword):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("the new password must differ from the current one"))
	case errors.Is(err, ErrWeakPassword), errors.Is(err, ErrBreachedPassword):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, errors.New("password reset failed"))
	}
}
