package user

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	commonv1 "github.com/riipandi/tango/codegen/proto/go/tango/common/v1"
	identityv1 "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1"
	identityv1connect "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1/identityv1connect"
	"github.com/riipandi/tango/modules/identity/password"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/riipandi/tango/pkg/responder"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "user"

// Module serves the account administration procedures: the RPC surface, and
// the REST routes the picture read and write claim. Authentication is the
// transport's bearer middleware for both — the module reads the claims the
// context carries, it never verifies a token itself.
type Module struct {
	service *Service
}

// NewModule builds the module over the account service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// MountRPC registers the procedures on the RPC router. The handler options
// are the transport's — the shared snake_case codec and the panic boundary —
// so the procedures answer exactly like the transport's own. Each procedure
// is registered at its own path: the generated handler answers a path under
// its prefix it does not know with a plain-text 404, which a Connect client
// cannot read.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := identityv1connect.NewUserServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(identityv1connect.UserServiceListUsersProcedure, handler)
	r.Handle(identityv1connect.UserServiceGetUserProcedure, handler)
	r.Handle(identityv1connect.UserServiceCreateUserProcedure, handler)
	r.Handle(identityv1connect.UserServiceUpdateUserProcedure, handler)
	r.Handle(identityv1connect.UserServiceDeleteUserProcedure, handler)
	r.Handle(identityv1connect.UserServiceResetProfilePictureProcedure, handler)
	r.Handle(identityv1connect.UserServiceGetCurrentUserProcedure, handler)
	r.Handle(identityv1connect.UserServiceUpdateCurrentUserProcedure, handler)
	r.Handle(identityv1connect.UserServiceDeleteMyAccountProcedure, handler)
	r.Handle(identityv1connect.UserServiceAddPasswordProcedure, handler)
	r.Handle(identityv1connect.UserServiceBanUserProcedure, handler)
	r.Handle(identityv1connect.UserServiceUnbanUserProcedure, handler)
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) identityv1connect.UserServiceHandler {
	return &rpcHandler{service: service}
}

// ListUsers answers one page of the accounts.
func (h *rpcHandler) ListUsers(ctx context.Context, req *connect.Request[identityv1.ListUsersRequest]) (*connect.Response[identityv1.ListUsersResponse], error) {
	// Absent a sort order the page answers newest first, the way the list
	// read before the sort key existed.
	ascending := req.Msg.GetSortOrder() == "asc"
	users, pagination, err := h.service.ListUsers(ctx, req.Msg.GetSearch(), req.Msg.GetSortBy(), ascending, int(req.Msg.GetPage()), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}

	views := make([]*identityv1.User, 0, len(users))
	for _, user := range users {
		views = append(views, WireView(user))
	}
	return connect.NewResponse(&identityv1.ListUsersResponse{
		Users:    views,
		Metadata: listMetadata(pagination),
		Status:   responder.StatusSuccess,
		Message:  "the users were listed",
	}), nil
}

// GetUser answers one account.
func (h *rpcHandler) GetUser(ctx context.Context, req *connect.Request[identityv1.GetUserRequest]) (*connect.Response[identityv1.GetUserResponse], error) {
	user, err := h.service.GetUser(ctx, req.Msg.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.GetUserResponse{
		User:    WireView(user),
		Status:  responder.StatusSuccess,
		Message: "the user was fetched",
	}), nil
}

// CreateUser creates an account directly, without a signup token.
func (h *rpcHandler) CreateUser(ctx context.Context, req *connect.Request[identityv1.CreateUserRequest]) (*connect.Response[identityv1.CreateUserResponse], error) {
	body := req.Msg
	user, err := h.service.CreateUser(ctx, CreateParams{
		Username:      body.Username,
		Email:         body.Email,
		Password:      body.GetPassword(),
		FirstName:     body.GetFirstName(),
		LastName:      body.GetLastName(),
		DisplayName:   body.GetDisplayName(),
		Locale:        body.GetLocale(),
		Disabled:      body.GetDisabled(),
		EmailVerified: body.GetEmailVerified(),
		GroupIDs:      body.GetUserGroupIds(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.CreateUserResponse{
		User:    WireView(user),
		Status:  responder.StatusSuccess,
		Message: "the user was created",
	}), nil
}

// UpdateUser replaces an account's fields.
func (h *rpcHandler) UpdateUser(ctx context.Context, req *connect.Request[identityv1.UpdateUserRequest]) (*connect.Response[identityv1.UpdateUserResponse], error) {
	body := req.Msg
	params := UpdateParams{
		Username:    body.Username,
		Email:       body.Email,
		FirstName:   body.FirstName,
		LastName:    body.LastName,
		DisplayName: body.DisplayName,
		Locale:      body.Locale,
		Timezone:    body.Timezone,
		Disabled:    body.Disabled,
		BanReason:   optional(body.GetBanReason()),
	}
	if body.BanExpires != nil {
		at := body.BanExpires.AsTime()
		params.BanExpiresAt = &at
	}
	user, err := h.service.UpdateUser(ctx, body.Id, params)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.UpdateUserResponse{
		User:    WireView(user),
		Status:  responder.StatusSuccess,
		Message: "the user was updated",
	}), nil
}

// BanUser applies a ban to one account.
func (h *rpcHandler) BanUser(ctx context.Context, req *connect.Request[identityv1.BanUserRequest]) (*connect.Response[identityv1.BanUserResponse], error) {
	params := BanParams{Reason: req.Msg.Reason}
	if req.Msg.ExpiresAt != nil {
		at := req.Msg.ExpiresAt.AsTime()
		params.ExpiresAt = &at
	}
	outcome, err := h.service.BanUser(ctx, req.Msg.Id, params)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.BanUserResponse{
		User:    WireView(outcome.User),
		Status:  responder.StatusSuccess,
		Message: banMessage(outcome.User, outcome.EndedSessions),
	}), nil
}

// UnbanUser lifts one account's ban.
func (h *rpcHandler) UnbanUser(ctx context.Context, req *connect.Request[identityv1.UnbanUserRequest]) (*connect.Response[identityv1.UnbanUserResponse], error) {
	outcome, err := h.service.UnbanUser(ctx, req.Msg.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.UnbanUserResponse{
		User:    WireView(outcome.User),
		Status:  responder.StatusSuccess,
		Message: "the ban was lifted",
	}), nil
}

// UnlockUser lifts one account's open lockout. The restrictions feature it
// belongs to ships with the unified-restrictions change; until then the
// procedure is scaffold and answers unimplemented — the Yaak row carries the
// (unimplemented) marker and no guard rule names it.
func (h *rpcHandler) UnlockUser(ctx context.Context, req *connect.Request[identityv1.UnlockUserRequest]) (*connect.Response[identityv1.UnlockUserResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not yet implemented"))
}

// banMessage answers the sentence the response carries: the expiry names
// itself when there is one, and the ended sessions are counted so the
// caller sees what the ban did beyond the row.
func banMessage(subject UserView, ended int) string {
	word := "the ban was applied"
	if subject.BannedAt != nil && subject.BanExpires != nil {
		word = "the ban was applied until " + subject.BanExpires.Format(time.RFC3339)
	} else if subject.BannedAt != nil {
		word = "the ban was applied without an end date"
	}
	if ended > 0 {
		return word + "; " + strconv.Itoa(ended) + " live session(s) were ended"
	}
	return word
}

// DeleteUser removes an account.
func (h *rpcHandler) DeleteUser(ctx context.Context, req *connect.Request[identityv1.DeleteUserRequest]) (*connect.Response[identityv1.DeleteUserResponse], error) {
	// The guard has already established that the caller is an administrator,
	// so the claims are here and the name they carry is what the service
	// compares the target against: an administrator may not delete the
	// account they are signed in as.
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	if err := h.service.DeleteUser(ctx, req.Msg.Id, caller.Username); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.DeleteUserResponse{
		Status:  responder.StatusSuccess,
		Message: "the user was deleted",
	}), nil
}

// ResetProfilePicture removes an account's picture. The procedure is
// self-service: the guard has already established that the account the
// request names is the caller's own.
func (h *rpcHandler) ResetProfilePicture(ctx context.Context, req *connect.Request[identityv1.ResetProfilePictureRequest]) (*connect.Response[identityv1.ResetProfilePictureResponse], error) {
	if err := h.service.ResetProfilePicture(ctx, req.Msg.Id); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.ResetProfilePictureResponse{
		Status:  responder.StatusSuccess,
		Message: "the profile picture was reset",
	}), nil
}

// GetCurrentUser answers the account the caller is. The request carries no
// target: the subject the bearer middleware verified is the account read.
func (h *rpcHandler) GetCurrentUser(ctx context.Context, req *connect.Request[identityv1.GetCurrentUserRequest]) (*connect.Response[identityv1.GetCurrentUserResponse], error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	user, err := h.service.GetCurrentUser(ctx, caller.UserID)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.GetCurrentUserResponse{
		User:    WireView(user),
		Status:  responder.StatusSuccess,
		Message: "the current user was fetched",
	}), nil
}

// UpdateCurrentUser replaces the signed-in account's own profile fields.
// The request carries no identifier on purpose: the caller is the account,
// and a target the request named would be a second identity to disagree.
func (h *rpcHandler) UpdateCurrentUser(ctx context.Context, req *connect.Request[identityv1.UpdateCurrentUserRequest]) (*connect.Response[identityv1.UpdateCurrentUserResponse], error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	body := req.Msg
	user, err := h.service.UpdateCurrentUser(ctx, caller.UserID, ProfileParams{
		Username:    body.Username,
		FirstName:   body.FirstName,
		LastName:    body.LastName,
		DisplayName: body.DisplayName,
		Locale:      body.Locale,
		Timezone:    body.Timezone,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.UpdateCurrentUserResponse{
		User:    WireView(user),
		Status:  responder.StatusSuccess,
		Message: "the current user was updated",
	}), nil
}

// DeleteMyAccount removes the signed-in account itself. The request carries
// no identifier on purpose — the caller is the account — and a delegated
// caller is refused at the boundary: the impersonating administrator is not
// the account, and the account's own removal is not a delegate's choice.
func (h *rpcHandler) DeleteMyAccount(ctx context.Context, req *connect.Request[identityv1.DeleteMyAccountRequest]) (*connect.Response[identityv1.DeleteMyAccountResponse], error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if caller.IsImpersonating() {
		return nil, mapError(ErrUserNotFound)
	}
	if err := h.service.DeleteMyAccount(ctx, caller.UserID); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.DeleteMyAccountResponse{
		Status: responder.StatusSuccess, Message: "the account was deleted",
	}), nil
}

// AddPassword sets the caller's first password credential. The proof rode
// the X-Tango-Reauthentication header the guard consumed; the impersonating
// administrator is refused at the boundary — the account's credential is
// not a delegate's choice.
func (h *rpcHandler) AddPassword(ctx context.Context, req *connect.Request[identityv1.AddPasswordRequest]) (*connect.Response[identityv1.AddPasswordResponse], error) {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if caller.IsImpersonating() {
		return nil, mapError(ErrUserNotFound)
	}
	if err := h.service.AddPassword(ctx, caller.UserID, req.Msg.NewPassword); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.AddPasswordResponse{
		Status:  responder.StatusSuccess,
		Message: "the password was added",
	}), nil
}

// RemovePassword deletes the caller's password credential. The removal and
// its last-credential refusal ship with the security-notices change; until
// then the procedure is scaffold and answers unimplemented — the Yaak row
// carries the (unimplemented) marker and no guard rule names it.
func (h *rpcHandler) RemovePassword(ctx context.Context, req *connect.Request[identityv1.RemovePasswordRequest]) (*connect.Response[identityv1.RemovePasswordResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not yet implemented"))
}

// listMetadata maps the responder's pagination onto the shared block. The
// wire fields are optional, so an unknown range is absent rather than zero.
func listMetadata(p responder.Pagination) *commonv1.ListMetadata {
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
		*dst = new(int32(value))
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
	case errors.Is(err, ErrUserNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	case errors.Is(err, ErrAccountExists):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("account already exists"))
	case errors.Is(err, ErrSelfDeletion):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("an administrator cannot delete the account they are signed in with"))
	case errors.Is(err, ErrBanInPast):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("the ban expiry is in the past"))
	case errors.Is(err, ErrTimezoneInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("unknown timezone"))
	case errors.Is(err, ErrUsernameInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("username is invalid"))
	case errors.Is(err, ErrGroupUnknown):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("unknown user group"))
	case errors.Is(err, ErrPicturesUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("picture storage is not available"))
	case errors.Is(err, password.ErrPasswordSet):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the account already holds a password"))
	case errors.Is(err, ErrCredentialUnwired):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the credential side is not available"))
	case isPasswordPolicy(err):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, errors.New("user operation failed"))
	}
}

// isPasswordPolicy reports whether the failure is the credential policy's
// refusal. The rule lives in the password package, so the check does too —
// the handler maps the answer without learning the policy's rules.
func isPasswordPolicy(err error) bool {
	return errors.Is(err, password.ErrWeakPassword) || errors.Is(err, password.ErrBreachedPassword)
}
