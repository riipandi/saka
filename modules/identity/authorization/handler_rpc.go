package authorization

import (
	"context"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	commonv1 "github.com/riipandi/tango/codegen/proto/go/tango/common/v1"
	identityv1 "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1"
	identityv1connect "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1/identityv1connect"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/riipandi/tango/pkg/responder"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "authorization"

// Module serves the authorization administration procedures: the RPC
// surface, all of it administrative. Authentication is the transport's
// bearer middleware; the guard's admin rule is the whole policy, because a
// surface that granted its own management could raise anything to itself.
type Module struct {
	service *Service
}

// NewModule builds the module over the authorization service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the module's endpoints on the router. The surface is RPC
// alone — roles, permissions, and grants are administrative objects the API
// manages whole — so there is nothing on the HTTP router to claim.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC router. The handler options
// are the transport's — the shared snake_case codec and the panic boundary —
// so the procedures answer exactly like the transport's own. Each procedure
// is registered at its own path: the generated handler answers a path under
// its prefix it does not know with a plain-text 404, which a Connect client
// cannot read.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := identityv1connect.NewAuthorizationServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(identityv1connect.AuthorizationServiceListPermissionsProcedure, handler)
	r.Handle(identityv1connect.AuthorizationServiceListRolesProcedure, handler)
	r.Handle(identityv1connect.AuthorizationServiceGetRoleProcedure, handler)
	r.Handle(identityv1connect.AuthorizationServiceCreateRoleProcedure, handler)
	r.Handle(identityv1connect.AuthorizationServiceUpdateRoleProcedure, handler)
	r.Handle(identityv1connect.AuthorizationServiceDeleteRoleProcedure, handler)
	r.Handle(identityv1connect.AuthorizationServiceSetRolePermissionsProcedure, handler)
	r.Handle(identityv1connect.AuthorizationServiceListUserRolesProcedure, handler)
	r.Handle(identityv1connect.AuthorizationServiceSetUserRolesProcedure, handler)
	r.Handle(identityv1connect.AuthorizationServiceListUserPermissionsProcedure, handler)
	r.Handle(identityv1connect.AuthorizationServiceSetUserPermissionsProcedure, handler)
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) identityv1connect.AuthorizationServiceHandler {
	return &rpcHandler{service: service}
}

// ListPermissions answers the permission catalog.
func (h *rpcHandler) ListPermissions(ctx context.Context, req *connect.Request[identityv1.ListPermissionsRequest]) (*connect.Response[identityv1.ListPermissionsResponse], error) {
	catalog, err := h.service.ListPermissions(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	permissions := make([]*identityv1.Permission, 0, len(catalog))
	for _, entry := range catalog {
		permissions = append(permissions, &identityv1.Permission{
			Slug:        entry.Slug,
			Description: entry.Description,
		})
	}
	return connect.NewResponse(&identityv1.ListPermissionsResponse{
		Permissions: permissions,
		Status:      responder.StatusSuccess,
		Message:     "the permission catalog was listed",
	}), nil
}

// ListRoles answers one page of the roles.
func (h *rpcHandler) ListRoles(ctx context.Context, req *connect.Request[identityv1.ListRolesRequest]) (*connect.Response[identityv1.ListRolesResponse], error) {
	sortBy := req.Msg.GetSortBy()
	ascending := req.Msg.GetSortOrder() != "desc"

	roles, pagination, err := h.service.ListRoles(
		ctx,
		req.Msg.GetSearch(), sortBy, ascending,
		int(req.Msg.GetPage()), int(req.Msg.GetLimit()),
	)
	if err != nil {
		return nil, mapError(err)
	}

	views := make([]*identityv1.Role, 0, len(roles))
	for _, role := range roles {
		views = append(views, wireRole(role))
	}
	return connect.NewResponse(&identityv1.ListRolesResponse{
		Roles:    views,
		Metadata: listMetadata(pagination),
		Status:   responder.StatusSuccess,
		Message:  "the roles were listed",
	}), nil
}

// GetRole answers one role with its permission slugs.
func (h *rpcHandler) GetRole(ctx context.Context, req *connect.Request[identityv1.GetRoleRequest]) (*connect.Response[identityv1.GetRoleResponse], error) {
	role, err := h.service.GetRole(ctx, req.Msg.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.GetRoleResponse{
		Role:    wireDetail(role),
		Status:  responder.StatusSuccess,
		Message: "the role was fetched",
	}), nil
}

// CreateRole defines a custom role.
func (h *rpcHandler) CreateRole(ctx context.Context, req *connect.Request[identityv1.CreateRoleRequest]) (*connect.Response[identityv1.CreateRoleResponse], error) {
	role, err := h.service.CreateRole(ctx, CreateParams{
		Name:        req.Msg.Name,
		Slug:        req.Msg.Slug,
		Description: req.Msg.Description,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.CreateRoleResponse{
		Role:    wireDetail(role),
		Status:  responder.StatusSuccess,
		Message: "the role was created",
	}), nil
}

// UpdateRole replaces a role's fields.
func (h *rpcHandler) UpdateRole(ctx context.Context, req *connect.Request[identityv1.UpdateRoleRequest]) (*connect.Response[identityv1.UpdateRoleResponse], error) {
	role, err := h.service.UpdateRole(ctx, req.Msg.Id, CreateParams{
		Name:        req.Msg.Name,
		Description: req.Msg.Description,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.UpdateRoleResponse{
		Role:    wireDetail(role),
		Status:  responder.StatusSuccess,
		Message: "the role was updated",
	}), nil
}

// DeleteRole removes a custom role.
func (h *rpcHandler) DeleteRole(ctx context.Context, req *connect.Request[identityv1.DeleteRoleRequest]) (*connect.Response[identityv1.DeleteRoleResponse], error) {
	if err := h.service.DeleteRole(ctx, req.Msg.Id); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.DeleteRoleResponse{
		Status:  responder.StatusSuccess,
		Message: "the role was deleted",
	}), nil
}

// SetRolePermissions replaces a role's permission set.
func (h *rpcHandler) SetRolePermissions(ctx context.Context, req *connect.Request[identityv1.SetRolePermissionsRequest]) (*connect.Response[identityv1.SetRolePermissionsResponse], error) {
	role, err := h.service.SetRolePermissions(ctx, req.Msg.Id, req.Msg.PermissionSlugs)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.SetRolePermissionsResponse{
		Role:    wireDetail(role),
		Status:  responder.StatusSuccess,
		Message: "the role permissions were updated",
	}), nil
}

// ListUserRoles answers the roles one account holds.
func (h *rpcHandler) ListUserRoles(ctx context.Context, req *connect.Request[identityv1.ListUserRolesRequest]) (*connect.Response[identityv1.ListUserRolesResponse], error) {
	roles, err := h.service.ListUserRoles(ctx, req.Msg.UserId)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.ListUserRolesResponse{
		UserId:  req.Msg.UserId,
		Roles:   wireRoles(roles),
		Status:  responder.StatusSuccess,
		Message: "the user's roles were fetched",
	}), nil
}

// SetUserRoles replaces the set of roles one account holds. The granter is
// the caller: the audit record names them from the context, and the grant
// row does too.
func (h *rpcHandler) SetUserRoles(ctx context.Context, req *connect.Request[identityv1.SetUserRolesRequest]) (*connect.Response[identityv1.SetUserRolesResponse], error) {
	roles, err := h.service.SetUserRoles(ctx, req.Msg.UserId, req.Msg.RoleIds, callerID(ctx))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.SetUserRolesResponse{
		UserId:  req.Msg.UserId,
		Roles:   wireRoles(roles),
		Status:  responder.StatusSuccess,
		Message: "the user's roles were updated",
	}), nil
}

// ListUserPermissions answers the permissions granted to one account
// directly.
func (h *rpcHandler) ListUserPermissions(ctx context.Context, req *connect.Request[identityv1.ListUserPermissionsRequest]) (*connect.Response[identityv1.ListUserPermissionsResponse], error) {
	slugs, err := h.service.ListUserPermissions(ctx, req.Msg.UserId)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.ListUserPermissionsResponse{
		UserId:          req.Msg.UserId,
		PermissionSlugs: slugs,
		Status:          responder.StatusSuccess,
		Message:         "the user's permissions were fetched",
	}), nil
}

// SetUserPermissions replaces the direct grants one account carries.
func (h *rpcHandler) SetUserPermissions(ctx context.Context, req *connect.Request[identityv1.SetUserPermissionsRequest]) (*connect.Response[identityv1.SetUserPermissionsResponse], error) {
	slugs, err := h.service.SetUserPermissions(ctx, req.Msg.UserId, req.Msg.PermissionSlugs, callerID(ctx))
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&identityv1.SetUserPermissionsResponse{
		UserId:          req.Msg.UserId,
		PermissionSlugs: slugs,
		Status:          responder.StatusSuccess,
		Message:         "the user's permissions were updated",
	}), nil
}

// callerID reads the caller's wire identifier, the granter a grant row
// names. A request without a caller cannot reach here — the guard refuses
// it first — so the empty answer is for the tests alone.
func callerID(ctx context.Context) string {
	caller, _ := jwtutils.CallerFrom(ctx)
	if caller == nil {
		return ""
	}
	return caller.UserID
}

// wireRole maps the list row onto the wire message the list answers with.
func wireRole(view RoleRow) *identityv1.Role {
	role := &identityv1.Role{
		Id:              view.ID.String(),
		Name:            view.Name,
		Slug:            view.Slug,
		Description:     derefString(view.Description),
		Type:            wireRoleType(view.Type),
		PermissionCount: int32Of(view.PermissionCount),
		CreatedAt:       view.CreatedAt.Format(time.RFC3339),
	}
	if view.UpdatedAt != nil {
		role.UpdatedAt = new(view.UpdatedAt.Format(time.RFC3339))
	}
	return role
}

// wireRoles maps the stored rows a per-user answer carries onto the wire
// messages. The rows hold no permission count — the count belongs to the
// role list, not to the account's — so the wire field is left at zero
// rather than being computed with a query per row.
func wireRoles(rows []RoleSchema) []*identityv1.Role {
	roles := make([]*identityv1.Role, 0, len(rows))
	for _, row := range rows {
		role := &identityv1.Role{
			Id:          row.ID.String(),
			Name:        row.Name,
			Slug:        row.Slug,
			Description: derefString(row.Description),
			Type:        wireRoleType(row.Type),
			CreatedAt:   row.CreatedAt.Format(time.RFC3339),
		}
		if row.UpdatedAt != nil {
			role.UpdatedAt = new(row.UpdatedAt.Format(time.RFC3339))
		}
		roles = append(roles, role)
	}
	return roles
}

// wireDetail maps the detail view onto the wire message the single-role
// procedures answer with.
func wireDetail(view RoleDetail) *identityv1.RoleDetail {
	detail := &identityv1.RoleDetail{
		Id:          view.ID.String(),
		Name:        view.Name,
		Slug:        view.Slug,
		Description: derefString(view.Description),
		Type:        wireRoleType(view.Type),
		Permissions: view.Permissions,
		CreatedAt:   view.CreatedAt.Format(time.RFC3339),
	}
	if view.UpdatedAt != nil {
		detail.UpdatedAt = new(view.UpdatedAt.Format(time.RFC3339))
	}
	return detail
}

// wireRoleType maps the row's type string onto the wire enum.
func wireRoleType(kind string) identityv1.RoleType {
	if kind == RoleTypeSystem {
		return identityv1.RoleType_ROLE_TYPE_SYSTEM
	}
	return identityv1.RoleType_ROLE_TYPE_CUSTOM
}

// derefString answers the pointer's value, or the empty string the optional
// wire field reads as absent.
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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
	case errors.Is(err, ErrRoleNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("role not found"))
	case errors.Is(err, ErrRoleExists):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("role already exists"))
	case errors.Is(err, ErrSystemRole):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the role is a system role"))
	case errors.Is(err, ErrRoleInUse):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the role is still held by accounts"))
	case errors.Is(err, ErrPermissionNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("permission not found"))
	case errors.Is(err, ErrUserNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("authorization operation failed"))
	}
}
