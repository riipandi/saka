package authorization

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/authz"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/responder"
)

// The failures the service defines. The handler maps them onto the codes the
// Connect protocol carries; the service defines what happened, not how it is
// answered.
var (
	// ErrRoleNotFound is an identifier or slug that names no role.
	ErrRoleNotFound = errors.New("authorization: role not found")

	// ErrRoleExists is a name or slug the unique indexes already hold.
	ErrRoleExists = errors.New("authorization: role name or slug already in use")

	// ErrSystemRole is a change a system role refuses: it cannot be deleted
	// or renamed, and its permission set is the seed's.
	ErrSystemRole = errors.New("authorization: the role is a system role")

	// ErrRoleInUse is a deletion of a role accounts still hold. The grants
	// must be lifted first, so a deletion cannot silently strip the
	// accounts that carried it.
	ErrRoleInUse = errors.New("authorization: the role is still held by accounts")

	// ErrPermissionNotFound is a slug the catalog does not declare — or, on
	// the write paths, one the permissions table does not hold. A grant
	// that names nothing would authorize nothing and lie on every screen
	// that showed it.
	ErrPermissionNotFound = errors.New("authorization: permission not found")

	// ErrUserNotFound is an account identifier that names no account. It is
	// the user feature's refusal, re-raised here so the per-user
	// procedures answer the not-found the account surfaces answer.
	ErrUserNotFound = user.ErrUserNotFound
)

// Service carries the rules of authorization administration: what a role
// is, what it may hold, and what a grant records. The repository carries
// the SQL.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// audit writes the record of every change, in the transaction that
	// changes it. A grant and the record of it commit together, so the log
	// cannot describe a grant that does not exist.
	audit *audit.Recorder
	log   *slog.Logger
	now   func() time.Time
}

// NewService builds the service over the shared pool.
func NewService(pool *datastore.Postgres, recorder *audit.Recorder, log *slog.Logger) *Service {
	return &Service{
		pool:  pool,
		repo:  NewRepository(),
		audit: recorder,
		log:   log,
		now:   time.Now,
	}
}

// ListPermissions answers the permission catalog as the database holds it —
// the rows the seed writes from the code's own list, so the response and
// the grants that reference these rows by id cannot disagree — narrowed by
// the request's search and resource, ordered as the caller asked.
func (s *Service) ListPermissions(ctx context.Context, search, resource, sortBy string, ascending bool) ([]PermissionSchema, error) {
	return s.repo.ListPermissions(ctx, s.pool, search, resource, sortBy, ascending)
}

// ListRoles answers one page of the roles, ordered as the caller asked,
// optionally filtered by a search term and a role kind.
func (s *Service) ListRoles(ctx context.Context, search, roleType, sortBy string, ascending bool, page, limit int) ([]RoleRow, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)

	rows, total, err := s.repo.ListRoles(ctx, s.pool, search, roleType, sortBy, ascending, responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	return rows, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
}

// GetRole answers one role with the permission slugs it carries.
func (s *Service) GetRole(ctx context.Context, id string) (RoleDetail, error) {
	roleID, err := parseRoleID(id)
	if err != nil {
		return RoleDetail{}, err
	}
	return s.readDetail(ctx, s.pool, roleID)
}

// CreateParams carries the fields a custom role is made of.
type CreateParams struct {
	Name        string
	Slug        string
	Description string
}

// CreateRole defines a custom role. The slug is unique and immutable; the
// role is born with no permissions and is granted them by
// SetRolePermissions. The created row is read back inside the transaction,
// so the answer carries what the database stored, not what the request
// said.
func (s *Service) CreateRole(ctx context.Context, params CreateParams) (RoleDetail, error) {
	var description *string
	if params.Description != "" {
		description = &params.Description
	}

	var created RoleDetail
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		id, createErr := s.repo.CreateRole(ctx, tx, RoleSchema{
			Name:        params.Name,
			Slug:        params.Slug,
			Description: description,
			Type:        RoleTypeCustom,
		})
		if errUniqueViolation(createErr) {
			return ErrRoleExists
		}
		if createErr != nil {
			return fmt.Errorf("authorization: create role: %w", createErr)
		}

		detail, detailErr := s.readDetail(ctx, tx, id)
		if detailErr != nil {
			return detailErr
		}
		created = detail

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventRoleCreated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceRole,
			ResourceID:   id.UUID(),
			Payload: map[string]string{
				"name": params.Name,
				"slug": params.Slug,
			},
		})
		return nil
	})
	if err != nil {
		return RoleDetail{}, err
	}
	return created, nil
}

// UpdateRole replaces a role's name and description. A system role refuses:
// its name is what the code and the claims reference, and its meaning is
// not an administrator's to change.
func (s *Service) UpdateRole(ctx context.Context, id string, params CreateParams) (RoleDetail, error) {
	roleID, err := parseRoleID(id)
	if err != nil {
		return RoleDetail{}, err
	}

	var updated RoleDetail
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		existing, getErr := s.repo.GetRole(ctx, tx, roleID)
		if errors.Is(getErr, datastore.ErrNoRows) {
			return ErrRoleNotFound
		}
		if getErr != nil {
			return getErr
		}
		if existing.Type == RoleTypeSystem {
			return ErrSystemRole
		}

		var description *string
		if params.Description != "" {
			description = &params.Description
		}
		row := existing
		row.Name = params.Name
		row.Description = description
		if _, updateErr := s.repo.UpdateRole(ctx, tx, row); errUniqueViolation(updateErr) {
			return ErrRoleExists
		} else if updateErr != nil {
			return updateErr
		}

		detail, detailErr := s.readDetail(ctx, tx, roleID)
		if detailErr != nil {
			return detailErr
		}
		updated = detail

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventRoleUpdated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceRole,
			ResourceID:   roleID.UUID(),
			Payload: map[string]string{
				"slug": existing.Slug,
				"name": params.Name,
			},
		})
		return nil
	})
	if err != nil {
		return RoleDetail{}, err
	}
	return updated, nil
}

// DeleteRole removes a custom role. A system role is refused, and so is a
// role accounts still hold — the grants must be lifted first, so the
// deletion strips nothing silently.
func (s *Service) DeleteRole(ctx context.Context, id string) error {
	roleID, err := parseRoleID(id)
	if err != nil {
		return err
	}

	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		existing, getErr := s.repo.GetRole(ctx, tx, roleID)
		if errors.Is(getErr, datastore.ErrNoRows) {
			return ErrRoleNotFound
		}
		if getErr != nil {
			return getErr
		}
		if existing.Type == RoleTypeSystem {
			return ErrSystemRole
		}

		holders, holdersErr := s.repo.CountActiveHolders(ctx, tx, roleID)
		if holdersErr != nil {
			return holdersErr
		}
		if holders > 0 {
			return ErrRoleInUse
		}

		deleted, deleteErr := s.repo.DeleteRole(ctx, tx, roleID)
		if deleteErr != nil {
			return deleteErr
		}
		if !deleted {
			return ErrRoleNotFound
		}

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventRoleDeleted,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceRole,
			ResourceID:   roleID.UUID(),
			Payload: map[string]string{
				"slug": existing.Slug,
			},
		})
		return nil
	})
}

// SetRolePermissions replaces the permission set one role carries. Every
// slug must be one the permissions table holds — the catalog's mirror — and
// a system role's set is the seed's, not this procedure's.
func (s *Service) SetRolePermissions(ctx context.Context, id string, slugs []string) (RoleDetail, error) {
	roleID, err := parseRoleID(id)
	if err != nil {
		return RoleDetail{}, err
	}

	var updated RoleDetail
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		existing, getErr := s.repo.GetRole(ctx, tx, roleID)
		if errors.Is(getErr, datastore.ErrNoRows) {
			return ErrRoleNotFound
		}
		if getErr != nil {
			return getErr
		}
		if existing.Type == RoleTypeSystem {
			return ErrSystemRole
		}

		permissionIDs, resolveErr := s.repo.ResolvePermissionIDs(ctx, tx, slugs)
		if resolveErr != nil {
			return resolveErr
		}

		if setErr := s.repo.SetRolePermissions(ctx, tx, roleID, permissionIDs); setErr != nil {
			return setErr
		}

		detail, detailErr := s.readDetail(ctx, tx, roleID)
		if detailErr != nil {
			return detailErr
		}
		updated = detail

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventRolePermissionsUpdated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceRole,
			ResourceID:   roleID.UUID(),
			Payload: map[string]string{
				"slug":             existing.Slug,
				"permission_count": fmt.Sprint(len(slugs)),
			},
		})
		return nil
	})
	if err != nil {
		return RoleDetail{}, err
	}
	return updated, nil
}

// ListUserRoles answers the roles one account actively holds. The account
// is read first: a malformed or unknown identifier is the not-found
// failure, so an account that exists answers even with an empty set.
func (s *Service) ListUserRoles(ctx context.Context, id string) ([]RoleSchema, error) {
	userID, err := user.UUIDFromWire(id)
	if err != nil {
		return nil, ErrUserNotFound
	}
	if err := s.accountExists(ctx, userID); err != nil {
		return nil, err
	}
	return s.repo.ListRolesOfUser(ctx, s.pool, userID)
}

// SetUserRoles replaces the set of roles one account holds. The roles it
// held and no longer does are revoked — stamped, not deleted, so the
// assignment's history survives — and the new ones are granted. The caller's
// identifier names the granter when the request carries one; the seed's
// grants arrive without it.
func (s *Service) SetUserRoles(ctx context.Context, id string, roleIDs []string, grantedBy string) ([]RoleSchema, error) {
	userID, err := user.UUIDFromWire(id)
	if err != nil {
		return nil, ErrUserNotFound
	}
	if existsErr := s.accountExists(ctx, userID); existsErr != nil {
		return nil, existsErr
	}
	ids := make([]RoleID, 0, len(roleIDs))
	for _, raw := range roleIDs {
		roleID, parseErr := ParseID(raw)
		if parseErr != nil {
			return nil, ErrRoleNotFound
		}
		ids = append(ids, roleID)
	}
	var granter *uuid.UUID
	if grantedBy != "" {
		if callerID, callerErr := user.UUIDFromWire(grantedBy); callerErr == nil {
			granter = &callerID
		}
	}
	now := s.now()

	var roles []RoleSchema
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if setErr := s.replaceUserRoles(ctx, tx, userID, ids, granter, now); setErr != nil {
			return setErr
		}

		read, readErr := s.repo.ListRolesOfUser(ctx, tx, userID)
		if readErr != nil {
			return readErr
		}
		roles = read

		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventUserRolesUpdated,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"role_count": fmt.Sprint(len(ids)),
			},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return roles, nil
}

// replaceUserRoles diffs the account's active grants against the requested
// set and moves the rows: the ones leaving are stamped revoked, the ones
// arriving are inserted. The diff runs in the caller's transaction, so a
// grant that changes between the read and the write is caught by the row
// lock the update takes.
func (s *Service) replaceUserRoles(ctx context.Context, tx datastore.Querier, userID uuid.UUID, ids []RoleID, granter *uuid.UUID, now time.Time) error {
	// Every named role must exist: a grant into nothing would make the
	// account wrong rather than merely ungranted. The check runs in the
	// caller's transaction, so a role deleted between the request's arrival
	// and its write is still refused.
	if len(ids) > 0 {
		keys := make([]any, 0, len(ids))
		for _, id := range ids {
			keys = append(keys, id.UUID())
		}
		cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
		cb.Select("count(*)")
		cb.From(authz.RolesTable)
		cb.Where(cb.In("id", keys...))

		query, args := cb.Build()
		var found int
		if err := tx.QueryRow(ctx, query, args...).Scan(&found); err != nil {
			return fmt.Errorf("authorization: count roles: %w", err)
		}
		if found != len(ids) {
			return ErrRoleNotFound
		}
	}

	active, err := s.repo.ListActiveRoleIDs(ctx, tx, userID)
	if err != nil {
		return err
	}

	wanted := make([]string, 0, len(ids))
	for _, id := range ids {
		wanted = append(wanted, id.UUID())
	}
	var revoke []uuid.UUID
	for _, id := range active {
		if !slices.Contains(wanted, id.String()) {
			revoke = append(revoke, id)
		}
	}
	var grant []uuid.UUID
	for _, raw := range wanted {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return err
		}
		if !slices.Contains(active, parsed) {
			grant = append(grant, parsed)
		}
	}

	if err := s.repo.RevokeRoles(ctx, tx, userID, revoke, granter, now); err != nil {
		return err
	}
	return s.repo.GrantRoles(ctx, tx, userID, grant, granter)
}

// ListUserPermissions answers the slugs granted to one account directly.
// The account is read first, the way ListUserRoles reads it.
func (s *Service) ListUserPermissions(ctx context.Context, id string) ([]string, error) {
	userID, err := user.UUIDFromWire(id)
	if err != nil {
		return nil, ErrUserNotFound
	}
	if err := s.accountExists(ctx, userID); err != nil {
		return nil, err
	}
	return s.repo.ListPermissionsOfUser(ctx, s.pool, userID)
}

// SetUserPermissions replaces the direct grants one account carries, the
// way SetUserRoles replaces its roles.
func (s *Service) SetUserPermissions(ctx context.Context, id string, slugs []string, grantedBy string) ([]string, error) {
	userID, err := user.UUIDFromWire(id)
	if err != nil {
		return nil, ErrUserNotFound
	}
	if existsErr := s.accountExists(ctx, userID); existsErr != nil {
		return nil, existsErr
	}
	var granter *uuid.UUID
	if grantedBy != "" {
		if callerID, callerErr := user.UUIDFromWire(grantedBy); callerErr == nil {
			granter = &callerID
		}
	}
	now := s.now()

	var granted []string
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		permissionIDs, resolveErr := s.repo.ResolvePermissionIDs(ctx, tx, slugs)
		if resolveErr != nil {
			return resolveErr
		}

		active, activeErr := s.repo.ListActivePermissionIDs(ctx, tx, userID)
		if activeErr != nil {
			return activeErr
		}
		var revoke []uuid.UUID
		for _, id := range active {
			if !slices.Contains(permissionIDs, id) {
				revoke = append(revoke, id)
			}
		}
		var grant []uuid.UUID
		for _, id := range permissionIDs {
			if !slices.Contains(active, id) {
				grant = append(grant, id)
			}
		}

		if revokeErr := s.repo.RevokePermissions(ctx, tx, userID, revoke, granter, now); revokeErr != nil {
			return revokeErr
		}
		if grantErr := s.repo.GrantPermissions(ctx, tx, userID, grant, granter); grantErr != nil {
			return grantErr
		}

		read, readErr := s.repo.ListPermissionsOfUser(ctx, tx, userID)
		if readErr != nil {
			return readErr
		}
		granted = read

		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventUserPermissionsUpdated,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"permission_count": fmt.Sprint(len(slugs)),
			},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return granted, nil
}

// readDetail answers the role and its permission slugs over the given query
// surface.
func (s *Service) readDetail(ctx context.Context, db datastore.Querier, roleID RoleID) (RoleDetail, error) {
	row, err := s.repo.GetRole(ctx, db, roleID)
	if errors.Is(err, datastore.ErrNoRows) {
		return RoleDetail{}, ErrRoleNotFound
	}
	if err != nil {
		return RoleDetail{}, err
	}

	slugs, err := s.repo.ListRolePermissions(ctx, db, roleID)
	if err != nil {
		return RoleDetail{}, err
	}
	return RoleDetail{RoleSchema: row, Permissions: slugs}, nil
}

// accountExists answers whether the identifier names an account. It is the
// not-found boundary the per-user procedures share, the way the group
// procedures answer theirs.
func (s *Service) accountExists(ctx context.Context, userID uuid.UUID) error {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1").From(user.UserTable).Where(sb.Equal("id", userID))

	query, args := sb.Build()
	var one int
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&one); errors.Is(err, datastore.ErrNoRows) {
		return ErrUserNotFound
	} else if err != nil {
		return fmt.Errorf("authorization: read account: %w", err)
	}
	return nil
}

// parseRoleID turns the request's identifier into the key the rows carry.
// The wire form is the TypeID the responses speak; a malformed identifier
// names no role, so it is the not-found failure the same as an unknown one.
func parseRoleID(id string) (RoleID, error) {
	parsed, err := ParseID(id)
	if err != nil {
		return RoleID{}, ErrRoleNotFound
	}
	return parsed, nil
}
