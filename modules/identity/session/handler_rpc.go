package session

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	authnv1 "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1"
	authnv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1/authnv1connect"
	commonv1 "github.com/riipandi/saka/codegen/proto/go/saka/common/v1"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
	"github.com/riipandi/saka/pkg/printext"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "session"

// Module serves the session lifecycle procedures: the RPC surface, all of it.
// Authentication is the transport's middleware — the module reads the caller
// the context carries, it never verifies a token itself.
type Module struct {
	service *Service
}

// NewModule builds the module over the session service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the module's endpoints on the router. The surface is RPC
// alone — a session is managed through the API the token authenticates — so
// there is nothing on the HTTP router to claim.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC router. The handler options
// are the transport's — the shared snake_case codec and the panic boundary —
// so the procedures answer exactly like the transport's own. Each procedure
// is registered at its own path: the generated handler answers a path under
// its prefix it does not know with a plain-text 404, which a Connect client
// cannot read.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := authnv1connect.NewSessionServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(authnv1connect.SessionServiceSignOutProcedure, handler)
	r.Handle(authnv1connect.SessionServiceGetSessionProcedure, handler)
	r.Handle(authnv1connect.SessionServiceListSessionsProcedure, handler)
	r.Handle(authnv1connect.SessionServiceRevokeSessionProcedure, handler)
	r.Handle(authnv1connect.SessionServiceRefreshProcedure, handler)
	r.Handle(authnv1connect.SessionServiceSignOutOtherSessionsProcedure, handler)
	r.Handle(authnv1connect.SessionServiceSignOutAllSessionsProcedure, handler)
	r.Handle(authnv1connect.SessionServiceImpersonateUserProcedure, handler)
	r.Handle(authnv1connect.SessionServiceStopImpersonatingProcedure, handler)
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes and the caller's identity:
// every procedure acts on the session the claims name, which the guard's
// session rule has already established is present.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) authnv1connect.SessionServiceHandler {
	return &rpcHandler{service: service}
}

// sessionCaller reads the caller the transport's middleware resolved. The
// guard's session rule has already refused a machine credential, an
// impersonated caller, and a token without the session claim, so reaching
// here means the claims name a live session; a missing caller is the wiring
// defect it always is, and it is refused rather than dereferenced.
func sessionCaller(ctx context.Context) *jwtutils.Caller {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok || caller == nil || caller.SessionID == "" {
		return nil
	}
	return caller
}

// SignOut ends the session the access token names. The message is the
// holder's, in the second person: a fresh sign-out says they are signed out,
// an already-ended one says the intent was satisfied without a write, and an
// expired one says the sign-out closed a book the window had already closed.
func (h *rpcHandler) SignOut(ctx context.Context, req *connect.Request[authnv1.SignOutRequest]) (*connect.Response[authnv1.SignOutResponse], error) {
	caller := sessionCaller(ctx)
	if caller == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}

	outcome, err := h.service.SignOut(ctx, caller.SessionID, caller.UserID)
	if err != nil {
		return nil, mapError(err)
	}

	message := "you have been signed out"
	switch {
	case outcome.Already:
		message = "you had already been signed out"
	case outcome.Expired:
		message = "you have been signed out; the session had already expired"
	}
	return connect.NewResponse(&authnv1.SignOutResponse{
		Status:  webutil.StatusSuccess,
		Message: message,
	}), nil
}

// SignOutOtherSessions ends every live session of the account except the one
// the access token names. The message is the holder's: the count is what the
// sweep actually ended, and an account holding nothing else answers the
// success its intent already is.
func (h *rpcHandler) SignOutOtherSessions(ctx context.Context, req *connect.Request[authnv1.SignOutOtherSessionsRequest]) (*connect.Response[authnv1.SignOutOtherSessionsResponse], error) {
	count, err := h.revokeBulk(ctx, h.service.SignOutOtherSessions)
	if err != nil {
		return nil, err
	}

	message := fmt.Sprintf("you have been signed out of %d other %s", count, printext.Plural(count, "session"))
	if count == 0 {
		message = "you had no other sessions to sign out"
	}
	return connect.NewResponse(&authnv1.SignOutOtherSessionsResponse{
		RevokedCount: narrowCount(count),
		Status:       webutil.StatusSuccess,
		Message:      message,
	}), nil
}

// SignOutAllSessions ends every live session of the account, the one the
// access token names included. The token pair the caller holds is not
// invalidated by the call — the access token expires on its own — so the
// message says what was stamped, not what the caller still carries.
func (h *rpcHandler) SignOutAllSessions(ctx context.Context, req *connect.Request[authnv1.SignOutAllSessionsRequest]) (*connect.Response[authnv1.SignOutAllSessionsResponse], error) {
	count, err := h.revokeBulk(ctx, h.service.SignOutAllSessions)
	if err != nil {
		return nil, err
	}

	message := fmt.Sprintf("you have been signed out of all %d %s", count, printext.Plural(count, "session"))
	if count == 0 {
		message = "you had no live sessions to sign out"
	}
	return connect.NewResponse(&authnv1.SignOutAllSessionsResponse{
		RevokedCount: narrowCount(count),
		Status:       webutil.StatusSuccess,
		Message:      message,
	}), nil
}

// revokeBulk runs the shared sweep and maps its one failure — the caller's
// session claims not resolving — onto the code the ended session answers.
func (h *rpcHandler) revokeBulk(ctx context.Context, sweep func(ctx context.Context, callerSession, callerID string) (int, error)) (int, error) {
	caller := sessionCaller(ctx)
	if caller == nil {
		return 0, connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}

	count, err := sweep(ctx, caller.SessionID, caller.UserID)
	if err != nil {
		return 0, mapError(err)
	}
	return count, nil
}

// GetSession answers the session the access token names.
func (h *rpcHandler) GetSession(ctx context.Context, req *connect.Request[authnv1.GetSessionRequest]) (*connect.Response[authnv1.GetSessionResponse], error) {
	caller := sessionCaller(ctx)
	if caller == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}

	row, view, err := h.service.GetSession(ctx, caller.SessionID)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.GetSessionResponse{
		Session: wireSession(row, caller.SessionID),
		User:    wireUser(view),
		Status:  webutil.StatusSuccess,
		Message: "the session was fetched",
	}), nil
}

// ListSessions answers one page of the account's sessions.
func (h *rpcHandler) ListSessions(ctx context.Context, req *connect.Request[authnv1.ListSessionsRequest]) (*connect.Response[authnv1.ListSessionsResponse], error) {
	caller := sessionCaller(ctx)
	if caller == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}

	rows, pagination, err := h.service.ListSessions(ctx, caller.SessionID, caller.UserID, int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}

	views := make([]*authnv1.Session, 0, len(rows))
	for _, row := range rows {
		views = append(views, wireSession(row, caller.SessionID))
	}
	return connect.NewResponse(&authnv1.ListSessionsResponse{
		Sessions: views,
		Metadata: metadataOf(pagination),
		Status:   webutil.StatusSuccess,
		Message:  "the sessions were listed",
	}), nil
}

// RevokeSession ends one of the account's sessions.
func (h *rpcHandler) RevokeSession(ctx context.Context, req *connect.Request[authnv1.RevokeSessionRequest]) (*connect.Response[authnv1.RevokeSessionResponse], error) {
	caller := sessionCaller(ctx)
	if caller == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}

	if err := h.service.RevokeSession(ctx, caller.SessionID, caller.UserID, req.Msg.Id); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.RevokeSessionResponse{
		Status:  webutil.StatusSuccess,
		Message: "the session was revoked",
	}), nil
}

// Refresh exchanges the refresh token for a fresh pair. The procedure rides
// the session guard like its siblings, but the credential it spends is the
// body's refresh token, not the caller's session — a client rotating its
// token on a schedule may hold an access token whose session is the very row
// the body names.
func (h *rpcHandler) Refresh(ctx context.Context, req *connect.Request[authnv1.RefreshRequest]) (*connect.Response[authnv1.RefreshResponse], error) {
	refreshed, err := h.service.Refresh(ctx, req.Msg.RefreshToken)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.RefreshResponse{
		AccessToken:      refreshed.AccessToken,
		TokenType:        refreshed.TokenType,
		AccessExpiresIn:  refreshed.AccessExpiresIn,
		RefreshExpiresIn: refreshed.RefreshExpiresIn,
		RefreshToken:     refreshed.RefreshToken,
		SessionId:        refreshed.SessionID,
		User:             wireUser(refreshed.User),
		Status:           webutil.StatusSuccess,
		Message:          "the token pair was refreshed",
	}), nil
}

// ImpersonateUser opens a delegated session on the target's behalf. The
// caller's identity is the actor pair's only source — the request cannot
// name an actor, because a request that could would be a delegation of an
// unknown kind.
func (h *rpcHandler) ImpersonateUser(ctx context.Context, req *connect.Request[authnv1.ImpersonateUserRequest]) (*connect.Response[authnv1.ImpersonateUserResponse], error) {
	caller := sessionCaller(ctx)
	if caller == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}

	refreshed, err := h.service.ImpersonateUser(ctx, caller.UserID, caller.Username, req.Msg.User, req.Msg.Reason)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.ImpersonateUserResponse{
		AccessToken:      refreshed.AccessToken,
		TokenType:        refreshed.TokenType,
		AccessExpiresIn:  refreshed.AccessExpiresIn,
		RefreshExpiresIn: refreshed.RefreshExpiresIn,
		RefreshToken:     refreshed.RefreshToken,
		SessionId:        refreshed.SessionID,
		User:             wireUser(refreshed.User),
		Status:           webutil.StatusSuccess,
		Message:          "the impersonated session was opened",
	}), nil
}

// StopImpersonating ends the delegated session and answers the actor's own
// fresh pair. The rule that admits a delegated caller here — the one place
// the guard lets one through — is what makes the way out reachable.
func (h *rpcHandler) StopImpersonating(ctx context.Context, req *connect.Request[authnv1.StopImpersonatingRequest]) (*connect.Response[authnv1.StopImpersonatingResponse], error) {
	caller := sessionCaller(ctx)
	if caller == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication state missing"))
	}

	refreshed, err := h.service.StopImpersonating(ctx, caller.SessionID, caller)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&authnv1.StopImpersonatingResponse{
		AccessToken:      refreshed.AccessToken,
		TokenType:        refreshed.TokenType,
		AccessExpiresIn:  refreshed.AccessExpiresIn,
		RefreshExpiresIn: refreshed.RefreshExpiresIn,
		RefreshToken:     refreshed.RefreshToken,
		SessionId:        refreshed.SessionID,
		User:             wireUser(refreshed.User),
		Status:           webutil.StatusSuccess,
		Message:          "the impersonated session was ended",
	}), nil
}

// wireSession maps the stored row onto the wire message. The request facts
// are what the row stored when it was opened, and `current` is the one field
// the row cannot answer — it is the caller's question, not the row's.
func wireSession(row SessionSchema, current string) *authnv1.Session {
	session := &authnv1.Session{
		Id:        row.ID.String(),
		Provider:  row.Provider,
		Remember:  row.Remember,
		CreatedAt: row.CreatedAt.Format(time.RFC3339),
		ExpiresAt: row.ExpiresAt.Format(time.RFC3339),
		Current:   row.ID.String() == current,
	}
	if row.UserAgent != "" {
		session.UserAgent = &row.UserAgent
	}
	if row.IPAddress != nil {
		session.IpAddress = new(row.IPAddress.String())
	}
	if row.RefreshedAt != nil {
		session.RefreshedAt = new(row.RefreshedAt.Format(time.RFC3339))
	}
	if row.RevokedAt != nil {
		session.RevokedAt = new(row.RevokedAt.Format(time.RFC3339))
	}
	return session
}

// wireUser maps the account view onto the tokens' account view, the shape the
// sign-in answered with.
func wireUser(view user.UserView) *authnv1.AuthenticatedUser {
	return &authnv1.AuthenticatedUser{
		Id:          view.ID,
		Username:    view.Username,
		Email:       view.Email,
		DisplayName: view.DisplayName,
	}
}

// narrowCount narrows the sweep's count onto the wire field. The wire field
// is int32; a count beyond it saturates rather than wrapping, and no account
// the rules allow can hold that many sessions.
func narrowCount(n int) int32 {
	if n > math.MaxInt32 || n < math.MinInt32 {
		return math.MaxInt32
	}
	return int32(n)
}

// metadataOf maps the responder's pagination onto the shared block. The
// wire fields are optional, so an unknown range is absent rather than zero.
func metadataOf(p webutil.Pagination) *commonv1.ListMetadata {
	meta := &commonv1.ListMetadata{}
	set := func(dst **int32, src *int) {
		if src == nil {
			return
		}
		// The wire field is int32; a total beyond it saturates rather than
		// wrapping, and no page the rules allow can reach the bound.
		value := *src
		if value > math.MaxInt32 || value < math.MinInt32 {
			value = math.MaxInt32
		}
		narrowed := int32(value)
		*dst = &narrowed
	}
	set(&meta.Page, p.Page)
	set(&meta.Limit, p.Limit)
	set(&meta.TotalPages, p.TotalPages)
	set(&meta.TotalItems, p.TotalItems)
	set(&meta.FirstItemIndex, p.FirstItemIndex)
	set(&meta.LastItemIndex, p.LastItemIndex)
	return meta
}

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose text
// names nothing a caller could aim at. A malformed field never reaches the
// service: the transport's validate interceptor refuses it with the typed
// violation details the contracts carry.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrSessionEnded):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("the session has ended"))
	case errors.Is(err, ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("session not found"))
	case errors.Is(err, ErrTargetNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	case errors.Is(err, ErrTargetAdmin):
		return connect.NewError(connect.CodePermissionDenied, errors.New("an administrator may not be impersonated"))
	case errors.Is(err, ErrTargetSelf):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("an account may not impersonate itself"))
	case errors.Is(err, ErrNotImpersonating):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the caller is not impersonating"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("session operation failed"))
	}
}
