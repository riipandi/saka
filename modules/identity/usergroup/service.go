package usergroup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/responder"
)

// The failures the service defines. The handler maps them onto the codes the
// Connect protocol carries; the service defines what happened, not how it is
// answered.
var (
	// ErrGroupNotFound is an identifier that names no group.
	ErrGroupNotFound = errors.New("usergroup: group not found")

	// ErrGroupExists is a name the unique index already holds.
	ErrGroupExists = errors.New("usergroup: group name already in use")

	// ErrMemberNotFound is a member identifier that names no account. The
	// group is not made wrong by a member that does not exist, so the
	// replacement is refused whole.
	ErrMemberNotFound = user.ErrUserNotFound

	// ErrUserExists is an account identifier the per-user membership
	// procedures refuse: the account the request names does not exist. It
	// is the same failure ErrMemberNotFound names — one account, two
	// directions — kept as an alias so each procedure reads by its own
	// direction.
	ErrUserExists = ErrMemberNotFound
)

// Service carries the rules of group administration: what a group is, who may
// hold it, and what a change records. The repository carries the SQL.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// audit writes the record of every group change, in the transaction that
	// changes the group. A deletion and the record of it commit together, so
	// the log cannot describe a group that still exists.
	audit *audit.Recorder
	log   *slog.Logger
}

// NewService builds the service over the shared pool.
func NewService(pool *datastore.Postgres, recorder *audit.Recorder, log *slog.Logger) *Service {
	return &Service{
		pool:  pool,
		repo:  NewRepository(),
		audit: recorder,
		log:   log,
	}
}

// GroupView is the group as the service answers it: the row plus the member
// count. The members travel with the detail views, not with the list.
type GroupView struct {
	GroupSchema
	UserCount int
}

// GroupDetailView is the group as the detail procedures answer it: the row,
// the member count, the members, and the client allowlist the group names.
type GroupDetailView struct {
	GroupSchema
	UserCount      int
	Members        []user.UserSchema
	AllowedClients []ClientRef
}

// CreateParams carries the fields a group is made of.
type CreateParams struct {
	Name        string
	DisplayName string
}

// ListGroups answers one page of the groups, ordered as the caller asked,
// optionally filtered by a search term.
func (s *Service) ListGroups(ctx context.Context, search, sortBy string, ascending bool, page, limit int) ([]GroupView, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)

	rows, total, err := s.repo.ListGroups(ctx, s.pool, search, sortBy, ascending, responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
	}

	views := make([]GroupView, 0, len(rows))
	for _, row := range rows {
		views = append(views, GroupView(row))
	}
	return views, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
}

// GetGroup answers one group with its members. The membership is read in the
// same pool the group is: the two queries are reads, so no transaction holds
// them together — a member added between them answers in the next call.
func (s *Service) GetGroup(ctx context.Context, id string) (GroupDetailView, error) {
	groupID, err := parseGroupID(id)
	if err != nil {
		return GroupDetailView{}, err
	}
	return s.readDetail(ctx, s.pool, groupID)
}

// CreateUserGroup creates a group. The duplicate name is the unique index's
// answer, read from the write's failure; the created row is read back inside
// the transaction so the view carries what the database stored, not what the
// request said.
func (s *Service) CreateUserGroup(ctx context.Context, params CreateParams) (GroupDetailView, error) {
	var created GroupDetailView
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		id, createErr := s.repo.CreateGroup(ctx, tx, GroupSchema{
			Name:        params.Name,
			DisplayName: params.DisplayName,
		})
		if errUniqueViolation(createErr) {
			return ErrGroupExists
		}
		if createErr != nil {
			return fmt.Errorf("usergroup: create: %w", createErr)
		}

		detail, detailErr := s.readDetail(ctx, tx, id)
		if detailErr != nil {
			return detailErr
		}
		created = detail

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventGroupCreated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceGroup,
			ResourceID:   id.UUID(),
			Payload: map[string]string{
				"name":         params.Name,
				"display_name": params.DisplayName,
			},
		})
		return nil
	})
	if err != nil {
		return GroupDetailView{}, err
	}
	return created, nil
}

// UpdateUserGroup replaces a group's two fields. The group is read first: it
// is the not-found check, and the payload names what the fields were.
func (s *Service) UpdateUserGroup(ctx context.Context, id string, params CreateParams) (GroupDetailView, error) {
	groupID, err := parseGroupID(id)
	if err != nil {
		return GroupDetailView{}, err
	}

	var updated GroupDetailView
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		existing, getErr := s.repo.GetGroup(ctx, tx, groupID)
		if errors.Is(getErr, datastore.ErrNoRows) {
			return ErrGroupNotFound
		}
		if getErr != nil {
			return getErr
		}

		row := existing
		row.Name = params.Name
		row.DisplayName = params.DisplayName
		if _, updateErr := s.repo.UpdateGroup(ctx, tx, row); errUniqueViolation(updateErr) {
			return ErrGroupExists
		} else if updateErr != nil {
			return updateErr
		}

		detail, detailErr := s.readDetail(ctx, tx, groupID)
		if detailErr != nil {
			return detailErr
		}
		updated = detail

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventGroupUpdated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceGroup,
			ResourceID:   groupID.UUID(),
			Payload: map[string]string{
				"name":         existing.Name,
				"display_name": existing.DisplayName,
			},
		})
		return nil
	})
	if err != nil {
		return GroupDetailView{}, err
	}
	return updated, nil
}

// DeleteUserGroup removes a group. The membership rows die with it by the
// cascade, so the payload records the member count the deletion takes away —
// the one fact about the group a later reader cannot reconstruct.
func (s *Service) DeleteUserGroup(ctx context.Context, id string) error {
	groupID, err := parseGroupID(id)
	if err != nil {
		return err
	}

	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		existing, getErr := s.repo.GetGroup(ctx, tx, groupID)
		if errors.Is(getErr, datastore.ErrNoRows) {
			return ErrGroupNotFound
		}
		if getErr != nil {
			return getErr
		}

		members, memberErr := s.repo.ListMembers(ctx, tx, groupID)
		if memberErr != nil {
			return memberErr
		}

		deleted, deleteErr := s.repo.DeleteGroup(ctx, tx, groupID)
		if deleteErr != nil {
			return deleteErr
		}
		if !deleted {
			return ErrGroupNotFound
		}

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventGroupDeleted,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceGroup,
			ResourceID:   groupID.UUID(),
			Payload: map[string]string{
				"name":       existing.Name,
				"member_ids": fmt.Sprint(len(members)),
			},
		})
		return nil
	})
}

// SetUserGroupMembers replaces a group's member set. The replacement, the
// read-back, and the record of it are one transaction: a member set that
// changed and the record saying so commit together.
func (s *Service) SetUserGroupMembers(ctx context.Context, id string, memberIDs []string) (GroupDetailView, error) {
	groupID, err := parseGroupID(id)
	if err != nil {
		return GroupDetailView{}, err
	}
	ids, err := parseMemberIDs(memberIDs)
	if err != nil {
		return GroupDetailView{}, err
	}

	var updated GroupDetailView
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if _, getErr := s.repo.GetGroup(ctx, tx, groupID); errors.Is(getErr, datastore.ErrNoRows) {
			return ErrGroupNotFound
		} else if getErr != nil {
			return getErr
		}

		if _, setErr := s.repo.SetMembers(ctx, tx, groupID, ids); setErr != nil {
			return setErr
		}

		detail, detailErr := s.readDetail(ctx, tx, groupID)
		if detailErr != nil {
			return detailErr
		}
		updated = detail

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventGroupMembersUpdated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceGroup,
			ResourceID:   groupID.UUID(),
			Payload: map[string]string{
				"member_ids": fmt.Sprint(len(ids)),
			},
		})
		return nil
	})
	if err != nil {
		return GroupDetailView{}, err
	}
	return updated, nil
}

// GetUserGroups answers the groups one account belongs to, ordered by the
// group's display name. The account is read first: a malformed or unknown
// identifier is the not-found failure, so an account that exists answers
// even with an empty set — belonging to no group is a state, not an error.
func (s *Service) GetUserGroups(ctx context.Context, id string) ([]GroupSchema, error) {
	userID, err := user.UUIDFromWire(id)
	if err != nil {
		return nil, ErrMemberNotFound
	}
	if err := s.accountExists(ctx, userID); err != nil {
		return nil, err
	}
	return s.repo.ListGroupsOfUser(ctx, s.pool, userID)
}

// accountExists answers whether the identifier names an account. It is the
// not-found boundary the per-user procedures share: a malformed identifier
// and an unknown one are the same refusal, the way the group procedures
// answer a group the database does not hold.
func (s *Service) accountExists(ctx context.Context, userID uuid.UUID) error {
	row := s.pool.QueryRow(ctx, "SELECT 1 FROM "+user.UserTable+" WHERE id = $1", userID)
	var one int
	if err := row.Scan(&one); errors.Is(err, datastore.ErrNoRows) {
		return ErrMemberNotFound
	} else if err != nil {
		return fmt.Errorf("usergroup: read account: %w", err)
	}
	return nil
}

// UpdateUserGroups replaces the set of groups one account belongs to. The
// replacement, the read-back, and the record of it are one transaction: a
// membership set that changed and the record saying so commit together.
// Every named group must exist — the repository's existence check runs
// inside this transaction, so a group deleted between the request's arrival
// and its write is still refused.
//
// The account is read before the transaction opens: an identifier that
// names no account is the not-found refusal, and the record the change
// writes names its account, so writing it for one that does not exist
// would abort the transaction the change ran in.
func (s *Service) UpdateUserGroups(ctx context.Context, id string, groupIDs []string) ([]GroupSchema, error) {
	userID, err := user.UUIDFromWire(id)
	if err != nil {
		return nil, ErrMemberNotFound
	}
	if existErr := s.accountExists(ctx, userID); existErr != nil {
		return nil, existErr
	}
	ids := make([]GroupID, 0, len(groupIDs))
	for _, raw := range groupIDs {
		groupID, parseErr := ParseID(raw)
		if parseErr != nil {
			return nil, ErrGroupNotFound
		}
		ids = append(ids, groupID)
	}

	var groups []GroupSchema
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if _, setErr := s.repo.SetUserGroups(ctx, tx, userID, ids); setErr != nil {
			return setErr
		}

		read, readErr := s.repo.ListGroupsOfUser(ctx, tx, userID)
		if readErr != nil {
			return readErr
		}
		groups = read

		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventUserGroupsUpdated,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"group_ids": fmt.Sprint(len(ids)),
			},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return groups, nil
}

// readDetail answers the group and its members over the given query surface.
func (s *Service) readDetail(ctx context.Context, db datastore.Querier, groupID GroupID) (GroupDetailView, error) {
	row, err := s.repo.GetGroup(ctx, db, groupID)
	if errors.Is(err, datastore.ErrNoRows) {
		return GroupDetailView{}, ErrGroupNotFound
	}
	if err != nil {
		return GroupDetailView{}, err
	}

	members, err := s.repo.ListMembers(ctx, db, groupID)
	if err != nil {
		return GroupDetailView{}, err
	}
	clients, err := s.repo.ListAllowedClients(ctx, db, IDToUUID(groupID))
	if err != nil {
		return GroupDetailView{}, err
	}
	return GroupDetailView{
		GroupSchema:    row,
		UserCount:      len(members),
		Members:        members,
		AllowedClients: clients,
	}, nil
}

// parseGroupID turns the request's identifier into the key the rows carry.
// The wire form is the TypeID the responses speak; a malformed identifier
// names no group, so it is the not-found failure the same as an unknown one.
func parseGroupID(id string) (GroupID, error) {
	parsed, err := ParseID(id)
	if err != nil {
		return GroupID{}, ErrGroupNotFound
	}
	return parsed, nil
}

// parseMemberIDs turns the request's member list into the keys the accounts
// carry. A malformed identifier names no account, so it is refused the same
// as an unknown one.
func parseMemberIDs(ids []string) ([]uuid.UUID, error) {
	parsed := make([]uuid.UUID, 0, len(ids))
	for _, raw := range ids {
		id, err := user.UUIDFromWire(raw)
		if err != nil {
			return nil, ErrMemberNotFound
		}
		parsed = append(parsed, id)
	}
	return parsed, nil
}
