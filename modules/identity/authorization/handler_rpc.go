package authorization

import (
	"context"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/go-chi/chi/v5"

	authzv1 "github.com/riipandi/saka/codegen/proto/go/saka/authz/v1"
	authzv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authz/v1/authzv1connect"
	commonv1 "github.com/riipandi/saka/codegen/proto/go/saka/common/v1"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/pkg/jwtutils"
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

// MountRPC registers the procedures on the RPC server. The server carries the
// transport's interceptors and the mount the shared snake_case codec, so the
// procedures answer exactly like the transport's own.
func (m *Module) MountRPC(server *connect.Server) {
	authzv1connect.RegisterAuthorizationServiceHandler(server, newRPCHandler(m.service))
}

// rpcHandler is the transport mapping of the procedures. The service carries
// the rules; this type carries the connect codes.
type rpcHandler struct {
	service *Service
}

// newRPCHandler builds the handler over the service.
func newRPCHandler(service *Service) authzv1connect.AuthorizationServiceHandler {
	return &rpcHandler{service: service}
}

// ListPermissions answers the permission catalog.
func (h *rpcHandler) ListPermissions(ctx context.Context, req *authzv1.ListPermissionsRequest) (*authzv1.ListPermissionsResponse, error) {
	ascending := req.GetSortOrder() != "desc"

	entries, err := h.service.ListPermissions(ctx, req.GetNocache(), req.GetSearch(), req.GetResource(), req.GetSortBy(), ascending)
	if err != nil {
		return nil, mapError(err)
	}
	permissions := make([]*authzv1.Permission, 0, len(entries))
	for _, entry := range entries {
		permissions = append(permissions, &authzv1.Permission{
			Id:          entry.ID.String(),
			Slug:        entry.Slug,
			Description: entry.Description,
		})
	}
	return &authzv1.ListPermissionsResponse{
		Permissions: permissions,
		Status:      webutil.StatusSuccess,
		Message:     "the permission catalog was listed",
	}, nil
}

// ListRoles answers one page of the roles.
func (h *rpcHandler) ListRoles(ctx context.Context, req *authzv1.ListRolesRequest) (*authzv1.ListRolesResponse, error) {
	sortBy := req.GetSortBy()
	ascending := req.GetSortOrder() != "desc"

	roles, pagination, err := h.service.ListRoles(
		ctx,
		req.GetNocache(),
		req.GetSearch(), parseRoleType(req.Type), sortBy, ascending,
		int(req.GetPage()), int(req.GetLimit()),
	)
	if err != nil {
		return nil, mapError(err)
	}

	views := make([]*authzv1.Role, 0, len(roles))
	for _, role := range roles {
		views = append(views, wireRole(role))
	}
	return &authzv1.ListRolesResponse{
		Roles:    views,
		Metadata: listMetadata(pagination),
		Status:   webutil.StatusSuccess,
		Message:  "the roles were listed",
	}, nil
}

// GetRole answers one role with its permission slugs.
func (h *rpcHandler) GetRole(ctx context.Context, req *authzv1.GetRoleRequest) (*authzv1.GetRoleResponse, error) {
	role, err := h.service.GetRole(ctx, req.GetNocache(), req.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return &authzv1.GetRoleResponse{
		Role:    wireDetail(role),
		Status:  webutil.StatusSuccess,
		Message: "the role was fetched",
	}, nil
}

// CreateRole defines a custom role.
func (h *rpcHandler) CreateRole(ctx context.Context, req *authzv1.CreateRoleRequest) (*authzv1.CreateRoleResponse, error) {
	role, err := h.service.CreateRole(ctx, CreateParams{
		Name:        req.Name,
		Slug:        req.Slug,
		Description: req.Description,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &authzv1.CreateRoleResponse{
		Role:    wireDetail(role),
		Status:  webutil.StatusSuccess,
		Message: "the role was created",
	}, nil
}

// UpdateRole replaces a role's fields.
func (h *rpcHandler) UpdateRole(ctx context.Context, req *authzv1.UpdateRoleRequest) (*authzv1.UpdateRoleResponse, error) {
	role, err := h.service.UpdateRole(ctx, req.Id, CreateParams{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &authzv1.UpdateRoleResponse{
		Role:    wireDetail(role),
		Status:  webutil.StatusSuccess,
		Message: "the role was updated",
	}, nil
}

// DeleteRole removes a custom role.
func (h *rpcHandler) DeleteRole(ctx context.Context, req *authzv1.DeleteRoleRequest) (*authzv1.DeleteRoleResponse, error) {
	if err := h.service.DeleteRole(ctx, req.Id); err != nil {
		return nil, mapError(err)
	}
	return &authzv1.DeleteRoleResponse{
		Status:  webutil.StatusSuccess,
		Message: "the role was deleted",
	}, nil
}

// SetRolePermissions replaces a role's permission set.
func (h *rpcHandler) SetRolePermissions(ctx context.Context, req *authzv1.SetRolePermissionsRequest) (*authzv1.SetRolePermissionsResponse, error) {
	role, err := h.service.SetRolePermissions(ctx, req.Id, req.PermissionSlugs)
	if err != nil {
		return nil, mapError(err)
	}
	return &authzv1.SetRolePermissionsResponse{
		Role:    wireDetail(role),
		Status:  webutil.StatusSuccess,
		Message: "the role permissions were updated",
	}, nil
}

// ListUserRoles answers the roles one account holds.
func (h *rpcHandler) ListUserRoles(ctx context.Context, req *authzv1.ListUserRolesRequest) (*authzv1.ListUserRolesResponse, error) {
	roles, err := h.service.ListUserRoles(ctx, req.UserId)
	if err != nil {
		return nil, mapError(err)
	}
	return &authzv1.ListUserRolesResponse{
		UserId:  req.UserId,
		Roles:   wireRoles(roles),
		Status:  webutil.StatusSuccess,
		Message: "the user's roles were fetched",
	}, nil
}

// SetUserRoles replaces the set of roles one account holds. The granter is
// the caller: the audit record names them from the context, and the grant
// row does too.
func (h *rpcHandler) SetUserRoles(ctx context.Context, req *authzv1.SetUserRolesRequest) (*authzv1.SetUserRolesResponse, error) {
	roles, err := h.service.SetUserRoles(ctx, req.UserId, req.RoleIds, callerID(ctx))
	if err != nil {
		return nil, mapError(err)
	}
	return &authzv1.SetUserRolesResponse{
		UserId:  req.UserId,
		Roles:   wireRoles(roles),
		Status:  webutil.StatusSuccess,
		Message: "the user's roles were updated",
	}, nil
}

// ListUserPermissions answers the permissions granted to one account
// directly.
func (h *rpcHandler) ListUserPermissions(ctx context.Context, req *authzv1.ListUserPermissionsRequest) (*authzv1.ListUserPermissionsResponse, error) {
	slugs, err := h.service.ListUserPermissions(ctx, req.UserId)
	if err != nil {
		return nil, mapError(err)
	}
	return &authzv1.ListUserPermissionsResponse{
		UserId:          req.UserId,
		PermissionSlugs: slugs,
		Status:          webutil.StatusSuccess,
		Message:         "the user's permissions were fetched",
	}, nil
}

// SetUserPermissions replaces the direct grants one account carries.
func (h *rpcHandler) SetUserPermissions(ctx context.Context, req *authzv1.SetUserPermissionsRequest) (*authzv1.SetUserPermissionsResponse, error) {
	slugs, err := h.service.SetUserPermissions(ctx, req.UserId, req.PermissionSlugs, callerID(ctx))
	if err != nil {
		return nil, mapError(err)
	}
	return &authzv1.SetUserPermissionsResponse{
		UserId:          req.UserId,
		PermissionSlugs: slugs,
		Status:          webutil.StatusSuccess,
		Message:         "the user's permissions were updated",
	}, nil
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
func wireRole(view RoleRow) *authzv1.Role {
	role := &authzv1.Role{
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
func wireRoles(rows []RoleSchema) []*authzv1.Role {
	roles := make([]*authzv1.Role, 0, len(rows))
	for _, row := range rows {
		role := &authzv1.Role{
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
func wireDetail(view RoleDetail) *authzv1.RoleDetail {
	detail := &authzv1.RoleDetail{
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
func wireRoleType(kind string) authzv1.RoleType {
	if kind == RoleTypeSystem {
		return authzv1.RoleType_ROLE_TYPE_SYSTEM
	}
	return authzv1.RoleType_ROLE_TYPE_CUSTOM
}

// parseRoleType reads the request's role-kind filter back into the row's
// vocabulary; an unset enum keeps every kind.
func parseRoleType(value *authzv1.RoleType) string {
	if value == nil {
		return ""
	}
	switch *value {
	case authzv1.RoleType_ROLE_TYPE_SYSTEM:
		return RoleTypeSystem
	case authzv1.RoleType_ROLE_TYPE_CUSTOM:
		return RoleTypeCustom
	default:
		return ""
	}
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
func listMetadata(p webutil.Pagination) *commonv1.ListMetadata {
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
		return connect.NewError(connect.CodeNotFound, "role not found")
	case errors.Is(err, ErrRoleExists):
		return connect.NewError(connect.CodeAlreadyExists, "role already exists")
	case errors.Is(err, ErrSystemRole):
		return connect.NewError(connect.CodeFailedPrecondition, "the role is a system role")
	case errors.Is(err, ErrRoleInUse):
		return connect.NewError(connect.CodeFailedPrecondition, "the role is still held by accounts")
	case errors.Is(err, ErrPermissionNotFound):
		return connect.NewError(connect.CodeNotFound, "permission not found")
	case errors.Is(err, ErrUserNotFound):
		return connect.NewError(connect.CodeNotFound, "user not found")
	default:
		return connect.NewError(connect.CodeInternal, "authorization operation failed")
	}
}
