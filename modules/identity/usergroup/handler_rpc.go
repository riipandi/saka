package usergroup

import (
	"context"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	commonv1 "github.com/riipandi/saka/codegen/proto/go/saka/common/v1"
	identityv1 "github.com/riipandi/saka/codegen/proto/go/saka/identity/v1"
	identityv1connect "github.com/riipandi/saka/codegen/proto/go/saka/identity/v1/identityv1connect"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/responder"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "usergroup"

// Module serves the group administration procedures: the RPC surface, all of
// it administrative. Authentication is the transport's bearer middleware.
type Module struct {
	service *Service
}

// NewModule builds the module over the group service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the module's endpoints on the router. The surface is RPC
// alone — a group is an administrative object the API manages whole — so
// there is nothing on the HTTP router to claim.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC router. The handler options
// are the transport's — the shared snake_case codec and the panic boundary —
// so the procedures answer exactly like the transport's own. Each procedure
// is registered at its own path: the generated handler answers a path under
// its prefix it does not know with a plain-text 404, which a Connect client
// cannot read.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := identityv1connect.NewUserGroupServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(identityv1connect.UserGroupServiceListUserGroupsProcedure, handler)
	r.Handle(identityv1connect.UserGroupServiceGetUserGroupProcedure, handler)
	r.Handle(identityv1connect.UserGroupServiceCreateUserGroupProcedure, handler)
	r.Handle(identityv1connect.UserGroupServiceUpdateUserGroupProcedure, handler)
	r.Handle(identityv1connect.UserGroupServiceDeleteUserGroupProcedure, handler)
	r.Handle(identityv1connect.UserGroupServiceSetUserGroupMembersProcedure, handler)
	r.Handle(identityv1connect.UserGroupServiceGetUserGroupsProcedure, handler)
	r.Handle(identityv1connect.UserGroupServiceUpdateUserGroupsProcedure, handler)
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) identityv1connect.UserGroupServiceHandler {
	return &rpcHandler{service: service}
}

// ListUserGroups answers one page of the groups.
func (h *rpcHandler) ListUserGroups(ctx context.Context, req *connect.Request[identityv1.ListUserGroupsRequest]) (*connect.Response[identityv1.ListUserGroupsResponse], error) {
	sortBy := req.Msg.GetSortBy()
	ascending := req.Msg.GetSortOrder() != "desc"

	groups, pagination, err := h.service.ListGroups(
		ctx,
		req.Msg.GetSearch(), sortBy, ascending,
		int(req.Msg.GetPage()), int(req.Msg.GetLimit()),
	)
	if err != nil {
		return nil, mapError(err)
	}

	views := make([]*identityv1.UserGroup, 0, len(groups))
	for _, group := range groups {
		views = append(views, wireGroup(group))
	}
	return connect.NewResponse(&identityv1.ListUserGroupsResponse{
		Groups:   views,
		Metadata: listMetadata(pagination),
		Status:   responder.StatusSuccess,
		Message:  "the user groups were listed",
	}), nil
}

// GetUserGroup answers one group with its members.
func (h *rpcHandler) GetUserGroup(ctx context.Context, req *connect.Request[identityv1.GetUserGroupRequest]) (*connect.Response[identityv1.GetUserGroupResponse], error) {
	group, err := h.service.GetGroup(ctx, req.Msg.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.GetUserGroupResponse{
		Group:   wireDetail(group),
		Status:  responder.StatusSuccess,
		Message: "the user group was fetched",
	}), nil
}

// CreateUserGroup creates a group.
func (h *rpcHandler) CreateUserGroup(ctx context.Context, req *connect.Request[identityv1.CreateUserGroupRequest]) (*connect.Response[identityv1.CreateUserGroupResponse], error) {
	group, err := h.service.CreateUserGroup(ctx, CreateParams{
		Name:        req.Msg.Name,
		DisplayName: req.Msg.DisplayName,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.CreateUserGroupResponse{
		Group:   wireDetail(group),
		Status:  responder.StatusSuccess,
		Message: "the user group was created",
	}), nil
}

// UpdateUserGroup replaces a group's fields.
func (h *rpcHandler) UpdateUserGroup(ctx context.Context, req *connect.Request[identityv1.UpdateUserGroupRequest]) (*connect.Response[identityv1.UpdateUserGroupResponse], error) {
	group, err := h.service.UpdateUserGroup(ctx, req.Msg.Id, CreateParams{
		Name:        req.Msg.Name,
		DisplayName: req.Msg.DisplayName,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.UpdateUserGroupResponse{
		Group:   wireDetail(group),
		Status:  responder.StatusSuccess,
		Message: "the user group was updated",
	}), nil
}

// DeleteUserGroup removes a group.
func (h *rpcHandler) DeleteUserGroup(ctx context.Context, req *connect.Request[identityv1.DeleteUserGroupRequest]) (*connect.Response[identityv1.DeleteUserGroupResponse], error) {
	if err := h.service.DeleteUserGroup(ctx, req.Msg.Id); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.DeleteUserGroupResponse{
		Status:  responder.StatusSuccess,
		Message: "the user group was deleted",
	}), nil
}

// SetUserGroupMembers replaces a group's member set.
func (h *rpcHandler) SetUserGroupMembers(ctx context.Context, req *connect.Request[identityv1.SetUserGroupMembersRequest]) (*connect.Response[identityv1.SetUserGroupMembersResponse], error) {
	group, err := h.service.SetUserGroupMembers(ctx, req.Msg.Id, req.Msg.UserIds)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.SetUserGroupMembersResponse{
		Group:   wireDetail(group),
		Status:  responder.StatusSuccess,
		Message: "the user group members were updated",
	}), nil
}

// GetUserGroups answers the groups one account belongs to.
func (h *rpcHandler) GetUserGroups(ctx context.Context, req *connect.Request[identityv1.GetUserGroupsRequest]) (*connect.Response[identityv1.GetUserGroupsResponse], error) {
	groups, err := h.service.GetUserGroups(ctx, req.Msg.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.GetUserGroupsResponse{
		Groups:  wireGroups(groups),
		Status:  responder.StatusSuccess,
		Message: "the user's groups were fetched",
	}), nil
}

// UpdateUserGroups replaces the set of groups one account belongs to.
func (h *rpcHandler) UpdateUserGroups(ctx context.Context, req *connect.Request[identityv1.UpdateUserGroupsRequest]) (*connect.Response[identityv1.UpdateUserGroupsResponse], error) {
	groups, err := h.service.UpdateUserGroups(ctx, req.Msg.Id, req.Msg.GroupIds)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.UpdateUserGroupsResponse{
		Groups:  wireGroups(groups),
		Status:  responder.StatusSuccess,
		Message: "the user's groups were updated",
	}), nil
}

// SetAllowedOidcClients replaces the group's client allowlist.
func (h *rpcHandler) SetAllowedOidcClients(ctx context.Context, req *connect.Request[identityv1.SetGroupAllowedOidcClientsRequest]) (*connect.Response[identityv1.SetGroupAllowedOidcClientsResponse], error) {
	group, allowed, err := h.service.SetAllowedOidcClients(ctx, req.Msg.Id, req.Msg.OidcClientIds)
	if err != nil {
		return nil, mapError(err)
	}
	refs := make([]*identityv1.AllowedOidcClientRef, 0, len(allowed))
	for _, client := range allowed {
		refs = append(refs, &identityv1.AllowedOidcClientRef{
			Id:   client.ID,
			Name: client.Name,
		})
	}
	return connect.NewResponse(&identityv1.SetGroupAllowedOidcClientsResponse{
		Group:          wireGroup(group),
		AllowedClients: refs,
		Status:         responder.StatusSuccess,
		Message:        "the group's allowed OIDC clients were updated",
	}), nil
}

// wireGroup maps the group view onto the wire message the list answers with.
func wireGroup(view GroupView) *identityv1.UserGroup {
	group := &identityv1.UserGroup{
		Id:          view.ID.String(),
		Name:        view.Name,
		DisplayName: view.DisplayName,
		UserCount:   int32Of(view.UserCount),
		CreatedAt:   view.CreatedAt.Format(time.RFC3339),
	}
	if view.UpdatedAt != nil {
		group.UpdatedAt = new(view.UpdatedAt.Format(time.RFC3339))
	}
	return group
}

// wireGroups maps the stored rows a per-user answer carries onto the wire
// messages. The rows hold no member count — the count belongs to the group
// list, not to the account's — so the wire field is left at zero rather
// than being computed with a query per row.
func wireGroups(rows []GroupSchema) []*identityv1.UserGroup {
	groups := make([]*identityv1.UserGroup, 0, len(rows))
	for _, row := range rows {
		group := &identityv1.UserGroup{
			Id:          row.ID.String(),
			Name:        row.Name,
			DisplayName: row.DisplayName,
			CreatedAt:   row.CreatedAt.Format(time.RFC3339),
		}
		if row.UpdatedAt != nil {
			group.UpdatedAt = new(row.UpdatedAt.Format(time.RFC3339))
		}
		groups = append(groups, group)
	}
	return groups
}

// wireDetail maps the detail view onto the wire message the single-group
// procedures answer with. The members are the account wire view the user
// feature writes once.
func wireDetail(view GroupDetailView) *identityv1.UserGroupDetail {
	members := make([]*identityv1.User, 0, len(view.Members))
	for _, member := range view.Members {
		members = append(members, user.WireView(user.ViewSchema(member)))
	}
	clients := make([]*identityv1.AllowedOidcClientRef, 0, len(view.AllowedClients))
	for _, client := range view.AllowedClients {
		clients = append(clients, &identityv1.AllowedOidcClientRef{Id: client.ID, Name: client.Name})
	}
	detail := &identityv1.UserGroupDetail{
		Id:                 view.ID.String(),
		Name:               view.Name,
		DisplayName:        view.DisplayName,
		Users:              members,
		UserCount:          int32Of(view.UserCount),
		CreatedAt:          view.CreatedAt.Format(time.RFC3339),
		AllowedOidcClients: clients,
	}
	if view.UpdatedAt != nil {
		detail.UpdatedAt = new(view.UpdatedAt.Format(time.RFC3339))
	}
	return detail
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
		// The pointer is to the wire's int32; the narrowed value is what
		// the caller reads.
		narrowed := int32Of(value)
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

// int32Of narrows an int to the wire's int32, saturating at the bounds.
func int32Of(value int) int32 {
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	if value < math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}

// mapError translates the service's failures into the codes the Connect
// protocol carries. The internal ones are collapsed to one answer whose text
// names nothing a caller could aim at. A malformed field never reaches the
// service: the transport's validate interceptor refuses it with the typed
// violation details the contracts carry.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrGroupNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("user group not found"))
	case errors.Is(err, ErrGroupExists):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("user group already exists"))
	case errors.Is(err, ErrMemberNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("user group operation failed"))
	}
}
