package usergroup

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/database/entity"
)

// ErrClientUnknown is an allowlist replacement naming a client that does
// not exist. The replacement is refused whole, so the group keeps the roll
// it held.
var ErrClientUnknown = errors.New("usergroup: a named OIDC client does not exist")

// ClientRef is one client the allowlist names, in the minimal shape a group
// view renders it: the protocol identifier and the display name.
type ClientRef struct {
	ID   string
	Name string
}

// SetAllowedClients replaces the group's client allowlist. The delete-then-
// insert runs in the caller's transaction, so the replacement is whole or
// nothing; the junction's foreign keys turn an unknown client or group into
// the refusal the service maps.
//
// The junction is the federation schema's table, and this feature writes it
// because the write is group-addressed — the mirror of the member set the
// group surface owns. The client feature reads the same rows at authorize
// time; neither feature imports the other.
func (r *Repository) SetAllowedClients(ctx context.Context, db datastore.Querier, groupID uuid.UUID, clientIDs []string) error {
	dbl := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbl.DeleteFrom(entity.TableUserGroupsAllowedOIDCClients)
	dbl.Where(dbl.Equal("user_group_id", groupID))

	query, args := dbl.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("usergroup: clear allowed clients: %w", err)
	}

	if len(clientIDs) == 0 {
		return nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUserGroupsAllowedOIDCClients)
	ib.Cols("user_group_id", "oidc_client_id")
	for _, clientID := range clientIDs {
		ib.Values(groupID, clientID)
	}

	query, args = ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrClientUnknown
		}
		return fmt.Errorf("usergroup: set allowed clients: %w", err)
	}
	return nil
}

// ListAllowedClients answers the clients the group's allowlist names,
// ordered by name.
func (r *Repository) ListAllowedClients(ctx context.Context, db datastore.Querier, groupID uuid.UUID) ([]ClientRef, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("j.oidc_client_id", "c.name")
	sb.From(entity.TableUserGroupsAllowedOIDCClients + " j")
	sb.JoinWithOption(sqlbuilder.LeftJoin, entity.TableOIDCClients+" c", "c.id = j.oidc_client_id")
	sb.Where(sb.Equal("j.user_group_id", groupID))
	sb.OrderBy("lower(c.name)", "j.oidc_client_id")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("usergroup: list allowed clients: %w", err)
	}
	defer rows.Close()

	clients := []ClientRef{}
	for rows.Next() {
		var id string
		var name *string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("usergroup: list allowed clients: %w", err)
		}
		// A client deleted between the junction's rows and this read has no
		// name to answer — the cascade removes it, but a racing read can
		// still see the row. The id alone is the honest answer.
		displayName := ""
		if name != nil {
			displayName = *name
		}
		clients = append(clients, ClientRef{ID: id, Name: displayName})
	}
	return clients, rows.Err()
}

// SetAllowedOidcClients replaces the group's client allowlist and answers
// the group with the roll it now names. Every write rides the audit record
// in the same transaction.
func (s *Service) SetAllowedOidcClients(ctx context.Context, id string, clientIDs []string) (GroupView, []ClientRef, error) {
	groupID, err := parseGroupID(id)
	if err != nil {
		return GroupView{}, nil, err
	}

	var group GroupView
	var allowed []ClientRef
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		detail, detailErr := s.readDetail(ctx, tx, groupID)
		if errors.Is(detailErr, datastore.ErrNoRows) {
			return ErrGroupNotFound
		}
		if detailErr != nil {
			return detailErr
		}

		if setErr := s.repo.SetAllowedClients(ctx, tx, IDToUUID(groupID), clientIDs); setErr != nil {
			return setErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventGroupAllowedClientsUpdated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceGroup,
			ResourceID:   groupID.UUID(),
			Payload:      map[string]string{"client_count": fmt.Sprintf("%d", len(clientIDs))},
		})
		group = GroupView{GroupSchema: detail.GroupSchema, UserCount: detail.UserCount}
		return nil
	})
	if err != nil {
		return GroupView{}, nil, err
	}

	allowed, err = s.repo.ListAllowedClients(ctx, s.pool, IDToUUID(groupID))
	if err != nil {
		return GroupView{}, nil, err
	}
	return group, allowed, nil
}
