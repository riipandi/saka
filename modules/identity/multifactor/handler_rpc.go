package multifactor

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/pkg/responder"

	"uuid"

	authnv1 "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1"
	authnv1connect "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1/authnv1connect"
	"github.com/riipandi/tango/modules/identity/signin"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/jwtutils"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// jwtutilsCallerFrom is the caller read the handler runs. It is the package
// function behind an alias so the file's imports stay honest about what it
// reaches for.
var jwtutilsCallerFrom = jwtutils.CallerFrom

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "multifactor"

// Module serves the multifactor procedures. Everything it answers is an RPC
// procedure, so its HTTP mount is empty by construction.
type Module struct {
	service *Service
}

// NewModule builds the module over the multifactor service.
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
	handler := newRPCHandler(m.service)
	_, connectHandler := authnv1connect.NewMultifactorServiceHandler(handler, opts...)
	r.Handle(authnv1connect.MultifactorServiceBeginTotpEnrollmentProcedure, connectHandler)
	r.Handle(authnv1connect.MultifactorServiceConfirmTotpEnrollmentProcedure, connectHandler)
	r.Handle(authnv1connect.MultifactorServiceListTotpEnrollmentsProcedure, connectHandler)
	r.Handle(authnv1connect.MultifactorServiceDeleteTotpEnrollmentProcedure, connectHandler)
	r.Handle(authnv1connect.MultifactorServiceCompleteSignInProcedure, connectHandler)
	r.Handle(authnv1connect.MultifactorServiceRegenerateRecoveryCodesProcedure, connectHandler)
	r.Handle(authnv1connect.MultifactorServiceDisableMfaProcedure, connectHandler)
	r.Handle(authnv1connect.MultifactorServiceVerifyRecoveryCodeProcedure, connectHandler)
	r.Handle(authnv1connect.MultifactorServiceAdminDisableMfaProcedure, connectHandler)
}

// rpcHandler is the transport mapping of the multifactor procedures. The
// service carries the rules; this type carries the connect codes and the
// request facts the protocol supplies on its own.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) authnv1connect.MultifactorServiceHandler {
	return &rpcHandler{service: service}
}

// callerID reads the account the access token names. The guard has already
// refused every caller the enrollment procedures do not admit, so the absent
// identity is the internal state a wiring bug produces, not a client error.
// The subject is the wire form — the `user_…` TypeID — so the row's UUID
// comes out through the user package's one conversion.
func callerID(ctx context.Context) (uuid.UUID, error) {
	caller, ok := jwtutilsCallerFrom(ctx)
	if !ok {
		return uuid.UUID{}, errors.New("multifactor: the caller is unnamed")
	}
	id, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return uuid.UUID{}, errors.New("multifactor: the caller's identity is unreadable")
	}
	return id, nil
}

// BeginTotpEnrollment writes an unconfirmed authenticator and answers its
// secret once.
func (h *rpcHandler) BeginTotpEnrollment(ctx context.Context, req *connect.Request[authnv1.BeginTotpEnrollmentRequest]) (*connect.Response[authnv1.BeginTotpEnrollmentResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	result, err := h.service.BeginTotpEnrollment(ctx, userID, req.Msg.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.BeginTotpEnrollmentResponse{
		TotpId:     result.TotpID,
		Name:       result.Name,
		Secret:     result.Secret,
		OtpauthUri: result.OTPAuthURI,
		ExpiresAt:  timestamppb.New(result.ExpiresAt),
	}), nil
}

// ConfirmTotpEnrollment activates the enrollment and answers the recovery
// set once.
func (h *rpcHandler) ConfirmTotpEnrollment(ctx context.Context, req *connect.Request[authnv1.ConfirmTotpEnrollmentRequest]) (*connect.Response[authnv1.ConfirmTotpEnrollmentResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	result, err := h.service.ConfirmTotpEnrollment(ctx, userID, req.Msg.TotpId, req.Msg.Code)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.ConfirmTotpEnrollmentResponse{
		TotpId:        result.TotpID,
		RecoveryCodes: result.RecoveryCodes,
	}), nil
}

// ListTotpEnrollments answers the account's authenticators.
func (h *rpcHandler) ListTotpEnrollments(ctx context.Context, req *connect.Request[authnv1.ListTotpEnrollmentsRequest]) (*connect.Response[authnv1.ListTotpEnrollmentsResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	rows, err := h.service.ListTotpEnrollments(ctx, userID)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*authnv1.TotpEnrollment, 0, len(rows))
	for _, row := range rows {
		view := &authnv1.TotpEnrollment{
			TotpId:      row.TotpID,
			Name:        row.Name,
			ConfirmedAt: timestampPtr(row.ConfirmedAt),
			LastUsedAt:  timestampPtr(row.LastUsedAt),
			CreatedAt:   timestamppb.New(row.CreatedAt),
		}
		// The secret is present only when the deployment's development aid
		// carries it; the wire field is optional, so empty stays absent.
		if row.Secret != "" {
			view.Secret = &row.Secret
		}
		out = append(out, view)
	}
	return connect.NewResponse(&authnv1.ListTotpEnrollmentsResponse{Enrollments: out}), nil
}

// DeleteTotpEnrollment removes one authenticator, with the proof the removal
// needs when it would disarm the account.
func (h *rpcHandler) DeleteTotpEnrollment(ctx context.Context, req *connect.Request[authnv1.DeleteTotpEnrollmentRequest]) (*connect.Response[authnv1.DeleteTotpEnrollmentResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	if err := h.service.DeleteTotpEnrollment(ctx, userID, req.Msg.TotpId, req.Msg.Code); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.DeleteTotpEnrollmentResponse{
		Message: "the authenticator was removed",
	}), nil
}

// CompleteSignIn spends the pending bridge plus the second factor on the
// session.
func (h *rpcHandler) CompleteSignIn(ctx context.Context, req *connect.Request[authnv1.CompleteSignInRequest]) (*connect.Response[authnv1.CompleteSignInResponse], error) {
	client := audit.ClientFromContext(ctx)

	result, err := h.service.CompleteSignIn(ctx, req.Msg.PendingToken, req.Msg.Code, signin.SessionParams{
		UserAgent:   client.UserAgent,
		IPAddress:   client.IPAddress,
		Fingerprint: client.Fingerprint,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.CompleteSignInResponse{
		AccessToken:      result.AccessToken,
		TokenType:        result.TokenType,
		AccessExpiresIn:  result.AccessExpiresIn,
		RefreshExpiresIn: result.RefreshExpiresIn,
		RefreshToken:     result.RefreshToken,
		SessionId:        result.SessionID,
		User: &authnv1.AuthenticatedUser{
			Id:          result.User.ID,
			Username:    result.User.Username,
			Email:       result.User.Email,
			DisplayName: result.User.DisplayName,
		},
		Status:  responder.StatusSuccess,
		Message: "the second factor verified and the session opened",
	}), nil
}

// RegenerateRecoveryCodes rewrites the set and answers it once.
func (h *rpcHandler) RegenerateRecoveryCodes(ctx context.Context, req *connect.Request[authnv1.RegenerateRecoveryCodesRequest]) (*connect.Response[authnv1.RegenerateRecoveryCodesResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	codes, err := h.service.RegenerateRecoveryCodes(ctx, userID, req.Msg.Code)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.RegenerateRecoveryCodesResponse{
		RecoveryCodes: codes,
	}), nil
}

// DisableMfa removes every factor after the proof.
func (h *rpcHandler) DisableMfa(ctx context.Context, req *connect.Request[authnv1.DisableMfaRequest]) (*connect.Response[authnv1.DisableMfaResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	if err := h.service.DisableMfa(ctx, userID, req.Msg.Code); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.DisableMfaResponse{
		Message: "multifactor authentication was disabled for the account",
	}), nil
}

// VerifyRecoveryCode spends one recovery code as the caller's standalone
// proof.
func (h *rpcHandler) VerifyRecoveryCode(ctx context.Context, req *connect.Request[authnv1.VerifyRecoveryCodeRequest]) (*connect.Response[authnv1.VerifyRecoveryCodeResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	if err := h.service.VerifyRecoveryCode(ctx, userID, req.Msg.Code); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.VerifyRecoveryCodeResponse{
		Status:  responder.StatusSuccess,
		Message: "the recovery code verified and is now spent",
	}), nil
}

// AdminDisableMfa removes the named account's every factor. The target is
// the request's wire-form identifier — the one conversion the user package
// owns — and the reason rides the audit record and the notification.
func (h *rpcHandler) AdminDisableMfa(ctx context.Context, req *connect.Request[authnv1.AdminDisableMfaRequest]) (*connect.Response[authnv1.AdminDisableMfaResponse], error) {
	targetID, err := user.UUIDFromWire(req.Msg.UserId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("the account is not found"))
	}

	if err := h.service.AdminDisableMfa(ctx, targetID, req.Msg.GetReason()); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.AdminDisableMfaResponse{
		Status:  responder.StatusSuccess,
		Message: "multifactor authentication was disabled for the account",
	}), nil
}

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose text
// names nothing a caller could aim at.
//
// The refusals a proof answers are the deliberate pair: a wrong code and a
// dead bridge both answer `unauthenticated` — the caller has proven nothing
// the procedure needs — and neither message says which half of the pair was
// the lie.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrCodeInvalid):
		return connect.NewError(connect.CodeUnauthenticated,
			errors.New("the code is not valid"))
	case errors.Is(err, ErrPendingInvalid):
		return connect.NewError(connect.CodeUnauthenticated,
			errors.New("the pending token is invalid or expired"))
	case errors.Is(err, ErrPendingExhausted):
		return connect.NewError(connect.CodeUnauthenticated,
			errors.New("the pending token is exhausted; sign in again"))
	case errors.Is(err, ErrEnrollmentNotFound):
		return connect.NewError(connect.CodeNotFound,
			errors.New("the enrollment is not found"))
	case errors.Is(err, ErrEnrollmentLimit):
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("the enrollment limit is reached"))
	case errors.Is(err, ErrEnrollmentExpired):
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("the enrollment has expired; start again"))
	case errors.Is(err, ErrNotConfirmed):
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("no confirmed authenticator is enrolled"))
	case errors.Is(err, ErrProofRequired):
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("the confirmation code is required for this removal"))
	case errors.Is(err, ErrNoRecoveryCodes):
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("no recovery codes are enrolled"))
	case errors.Is(err, ErrUserNotFound):
		return connect.NewError(connect.CodeNotFound,
			errors.New("the account is not found"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
}

// timestampPtr renders an optional time; nil stays absent on the wire.
func timestampPtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
