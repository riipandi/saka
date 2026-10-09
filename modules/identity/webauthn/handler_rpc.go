package webauthn

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	"github.com/go-chi/chi/v5"

	"uuid"

	"google.golang.org/protobuf/types/known/timestamppb"

	authnv1 "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1"
	authnv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1/authnv1connect"
	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/modules/identity/signin"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "webauthn"

// Module serves the webauthn procedures. Everything it answers is an RPC
// procedure, so its HTTP mount is empty by construction.
type Module struct {
	service *Service
}

// NewModule builds the module over the webauthn service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the endpoints on the HTTP router. The feature serves
// no plain HTTP route: a procedure is POST-only on the RPC surface.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC server. The server carries the
// transport's interceptors and the mount the shared snake_case codec, so the
// procedure answers exactly like the transport's own.
func (m *Module) MountRPC(server *connect.Server) {
	authnv1connect.RegisterWebAuthnServiceHandler(server, newRPCHandler(m.service))
}

// rpcHandler is the transport mapping of the webauthn procedures. The
// service carries the rules; this type carries the connect codes and the
// request facts the protocol supplies on its own.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) authnv1connect.WebAuthnServiceHandler {
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
		return uuid.UUID{}, errors.New("webauthn: the caller is unnamed")
	}
	id, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return uuid.UUID{}, errors.New("webauthn: the caller is unnamed")
	}
	return id, nil
}

// BeginRegistration opens the enrollment ceremony on the caller's account.
func (h *rpcHandler) BeginRegistration(ctx context.Context, _ *authnv1.BeginRegistrationRequest) (*authnv1.BeginRegistrationResponse, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "internal error")
	}

	options, sessionID, err := h.service.BeginRegistration(ctx, userID)
	if err != nil {
		return nil, mapError(err)
	}
	return &authnv1.BeginRegistrationResponse{
		Options:   options,
		SessionId: sessionID,
	}, nil
}

// VerifyRegistration finishes the enrollment with what the browser produced.
func (h *rpcHandler) VerifyRegistration(ctx context.Context, req *authnv1.VerifyRegistrationRequest) (*authnv1.VerifyRegistrationResponse, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "internal error")
	}

	enrolled, err := h.service.VerifyRegistration(ctx, userID, req.SessionId, req.Credential, req.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return &authnv1.VerifyRegistrationResponse{
		Credential: credentialView(enrolled),
	}, nil
}

// BeginLogin opens the usernameless sign-in ceremony.
func (h *rpcHandler) BeginLogin(ctx context.Context, _ *authnv1.BeginLoginRequest) (*authnv1.BeginLoginResponse, error) {
	options, sessionID, err := h.service.BeginLogin(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return &authnv1.BeginLoginResponse{
		Options:   options,
		SessionId: sessionID,
	}, nil
}

// VerifyLogin finishes the sign-in and answers the token pair.
func (h *rpcHandler) VerifyLogin(ctx context.Context, req *authnv1.VerifyLoginRequest) (*authnv1.VerifyLoginResponse, error) {
	client := fwaudit.ClientFromContext(ctx)
	result, err := h.service.VerifyLogin(ctx, req.SessionId, req.Credential, signin.SessionParams{
		UserAgent:   client.UserAgent,
		IPAddress:   client.IPAddress,
		Fingerprint: client.Fingerprint,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &authnv1.VerifyLoginResponse{
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
	}, nil
}

// ListCredentials answers the caller's passkey roll.
func (h *rpcHandler) ListCredentials(ctx context.Context, _ *authnv1.ListCredentialsRequest) (*authnv1.ListCredentialsResponse, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "internal error")
	}

	roll, err := h.service.ListCredentials(ctx, userID)
	if err != nil {
		return nil, mapError(err)
	}
	views := make([]*authnv1.Credential, 0, len(roll))
	for _, entry := range roll {
		views = append(views, credentialView(entry))
	}
	return &authnv1.ListCredentialsResponse{Credentials: views}, nil
}

// UpdateCredential renames one of the caller's passkeys.
func (h *rpcHandler) UpdateCredential(ctx context.Context, req *authnv1.UpdateCredentialRequest) (*authnv1.UpdateCredentialResponse, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "internal error")
	}

	renamed, err := h.service.RenameCredential(ctx, userID, req.CredentialId, req.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return &authnv1.UpdateCredentialResponse{
		Credential: credentialView(renamed),
	}, nil
}

// DeleteCredential removes one of the caller's passkeys.
func (h *rpcHandler) DeleteCredential(ctx context.Context, req *authnv1.DeleteCredentialRequest) (*authnv1.DeleteCredentialResponse, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "internal error")
	}

	if err := h.service.DeleteCredential(ctx, userID, req.CredentialId); err != nil {
		return nil, mapError(err)
	}
	return &authnv1.DeleteCredentialResponse{}, nil
}

// AdminListCredentials names one account's passkeys.
func (h *rpcHandler) AdminListCredentials(ctx context.Context, req *authnv1.AdminListCredentialsRequest) (*authnv1.ListCredentialsResponse, error) {
	roll, err := h.service.AdminListCredentials(ctx, req.UserId)
	if err != nil {
		return nil, mapError(err)
	}
	views := make([]*authnv1.Credential, 0, len(roll))
	for _, entry := range roll {
		views = append(views, credentialView(entry))
	}
	return &authnv1.ListCredentialsResponse{Credentials: views}, nil
}

// AdminUpdateCredential renames one of an account's passkeys.
func (h *rpcHandler) AdminUpdateCredential(ctx context.Context, req *authnv1.AdminUpdateCredentialRequest) (*authnv1.UpdateCredentialResponse, error) {
	renamed, err := h.service.AdminRenameCredential(ctx, req.UserId, req.CredentialId, req.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return &authnv1.UpdateCredentialResponse{
		Credential: credentialView(renamed),
	}, nil
}

// AdminDeleteCredential removes one of an account's passkeys.
func (h *rpcHandler) AdminDeleteCredential(ctx context.Context, req *authnv1.AdminDeleteCredentialRequest) (*authnv1.DeleteCredentialResponse, error) {
	if err := h.service.AdminDeleteCredential(ctx, req.UserId, req.CredentialId); err != nil {
		return nil, mapError(err)
	}
	return &authnv1.DeleteCredentialResponse{}, nil
}

// credentialView maps the service's view into the wire message.
func credentialView(entry View) *authnv1.Credential {
	view := &authnv1.Credential{
		Id:             entry.ID,
		Name:           entry.Name,
		Aaguid:         entry.AAGUID,
		BackupEligible: entry.BackupEligible,
		BackupState:    entry.BackupState,
		Transports:     entry.Transports,
		SignCount:      entry.SignCount,
		CreatedAt:      timestamppb.New(entry.CreatedAt),
	}
	if entry.LastUsedAt != nil {
		view.LastUsedAt = timestamppb.New(*entry.LastUsedAt)
	}
	return view
}

// Reauthenticate re-proves the caller and answers the single-use token the
// next guarded call spends through its header.
func (h *rpcHandler) Reauthenticate(ctx context.Context, req *authnv1.ReauthenticateRequest) (*authnv1.ReauthenticateResponse, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "internal error")
	}

	var password, sessionID, credential, emailCode string
	switch proof := req.Proof.(type) {
	case *authnv1.ReauthenticateRequest_Password:
		password = proof.Password
	case *authnv1.ReauthenticateRequest_Passkey:
		sessionID, credential = proof.Passkey.SessionId, proof.Passkey.Credential
	case *authnv1.ReauthenticateRequest_EmailCode:
		emailCode = proof.EmailCode
	}

	token, expiresAt, err := h.service.Reauthenticate(ctx, userID, password, sessionID, credential, emailCode)
	if err != nil {
		return nil, mapError(err)
	}
	return &authnv1.ReauthenticateResponse{
		Token:     token,
		ExpiresAt: timestamppb.New(expiresAt),
	}, nil
}

// SendReauthenticationCode delivers the email-code reverification factor to
// the caller's own address. The success says nothing about the account; the
// unavailable-delivery refusal is a failed-precondition the SPA routes to a
// notice, not an internal error it cannot act on.
func (h *rpcHandler) SendReauthenticationCode(ctx context.Context, req *authnv1.SendReauthenticationCodeRequest) (*authnv1.SendReauthenticationCodeResponse, error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "internal error")
	}
	if err := h.service.SendReauthenticationCode(ctx, userID); err != nil {
		return nil, mapError(err)
	}
	return &authnv1.SendReauthenticationCodeResponse{}, nil
}

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose text
// names nothing a caller could aim at.
//
// The ceremony refusals are the deliberate pair: an unknown, spent, or
// expired ceremony handle and a failed verification both answer
// `unauthenticated` — the caller has proven nothing the procedure needs —
// and neither message says which half was the lie.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrCeremonyInvalid):
		return connect.NewError(connect.CodeUnauthenticated,
			"the ceremony is invalid or expired; start again")
	case errors.Is(err, ErrAssertionInvalid):
		return connect.NewError(connect.CodeUnauthenticated,
			"the credential response failed verification")
	case errors.Is(err, ErrVerificationDue):
		return connect.NewError(connect.CodeInvalidArgument,
			"user verification is required; try again with verification")
	case errors.Is(err, ErrSyncedPasskeyOff):
		return connect.NewError(connect.CodeFailedPrecondition,
			"synced passkeys are not allowed by this deployment")
	case errors.Is(err, ErrTooManyPasskeys), errors.Is(err, ErrTooManyEnrollments):
		return connect.NewError(connect.CodeFailedPrecondition,
			"the enrollment limit is reached")
	case errors.Is(err, ErrSettingUnreadable):
		return connect.NewError(connect.CodeFailedPrecondition,
			"the passkey settings are unreadable; contact the operator")
	case errors.Is(err, ErrCredentialForeign):
		return connect.NewError(connect.CodeNotFound,
			"the credential is not found")
	case errors.Is(err, ErrAccountUnknown):
		return connect.NewError(connect.CodeNotFound,
			"the account is not found")
	case errors.Is(err, ErrCodeSendUnavailable):
		return connect.NewError(connect.CodeFailedPrecondition,
			"the code delivery is not available on this deployment")
	case errors.Is(err, ErrResendTooSoon):
		return connect.NewError(connect.CodeFailedPrecondition,
			"a code was sent recently; wait before asking again")
	case errors.Is(err, ErrClonedCredential):
		return connect.NewError(connect.CodeFailedPrecondition,
			"the credential is refused; contact the operator")
	case errors.Is(err, ErrCredentialDuplicate):
		return connect.NewError(connect.CodeFailedPrecondition,
			"the credential is already enrolled")
	case errors.Is(err, ErrLastWayIn):
		return connect.NewError(connect.CodeFailedPrecondition,
			"removing this credential would leave the account no way in")
	case errors.Is(err, ErrProofRefused):
		return connect.NewError(connect.CodeUnauthenticated,
			"the proof failed")
	case errors.Is(err, signin.ErrAccountDisabled):
		return connect.NewError(connect.CodeUnauthenticated,
			"the account is disabled")
	case errors.Is(err, signin.ErrAccountBanned):
		return connect.NewError(connect.CodeUnauthenticated,
			"the account is banned")
	case errors.Is(err, signin.ErrSigninRestricted):
		return connect.NewError(connect.CodePermissionDenied,
			"sign-in is not permitted")
	default:
		return connect.NewError(connect.CodeInternal, "internal error")
	}
}

// jwtutilsCallerFrom is the caller read the handler runs. It is the package
// function behind an alias so the file's imports stay honest about what it
// reaches for.
var jwtutilsCallerFrom = jwtutils.CallerFrom
