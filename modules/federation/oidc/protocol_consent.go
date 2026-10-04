package oidc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"encoding/json/v2"
	"slices"

	sqlbuilder "github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/database/entity"
)

// The consent ledger: user_authorized_oidc_clients answers the question
// the authorization flow asks — has this account agreed to this client
// for these scopes already — and records the agreement when it is made.

// accountAdmitted answers whether the client's group restriction lets the
// account in. A client is restricted when its restriction flag is set or its
// allowed-groups roll carries rows; a restricted client admits only accounts
// that belong to at least one of those groups — flag set with an empty roll
// admits nobody.
func (s *Service) accountAdmitted(ctx context.Context, userID, clientID string, restricted bool) (bool, error) {
	if !restricted {
		return true, nil
	}
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("EXISTS (SELECT 1 FROM public.oidc_clients_allowed_user_groups g " +
		"JOIN public.user_groups_users m ON m.user_group_id = g.user_group_id " +
		"WHERE g.oidc_client_id = " + sb.Var(clientID) +
		" AND m.user_id = " + sb.Var(userID) + ")")
	query, args := sb.Build()

	var admitted bool
	err := s.pool.QueryRow(ctx, query, args...).Scan(&admitted)
	if err != nil {
		return false, fmt.Errorf("oidc: check group restriction: %w", err)
	}
	return admitted, nil
}

// authorizedScopes reads the scopes an earlier consent stored.
func (s *Service) authorizedScopes(ctx context.Context, userID, clientID string) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("scope").From(entity.TableUserAuthorizedOIDCClients).
		Where(sb.Equal("user_id", userID), sb.Equal("client_id", clientID))
	sql, args := sb.Build()

	var raw []byte
	err := s.pool.QueryRow(ctx, sql, args...).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return decodeScopeJSON(raw), nil
}

// recordAuthorization stores (or widens) the account's consent for a
// client, the write the approval commits.
func (s *Service) recordAuthorization(ctx context.Context, userID, clientID string, approved []string) error {
	known, err := s.authorizedScopes(ctx, userID, clientID)
	if err != nil {
		return err
	}
	for _, scope := range approved {
		if !slices.Contains(known, scope) {
			known = append(known, scope)
		}
	}
	now := time.Now().UTC()
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(entity.TableUserAuthorizedOIDCClients)
	sb.Cols("user_id", "client_id", "scope", "last_used_at")
	sb.Values(userID, clientID, encodeScopeJSON(known), now)
	query, args := sb.Build()
	query += " ON CONFLICT (user_id, client_id) DO UPDATE SET scope = EXCLUDED.scope, last_used_at = EXCLUDED.last_used_at"
	_, err = s.pool.Exec(ctx, query, args...)
	return err
}

// encodeScopeJSON renders the scope list as the JSONB column stores it.
func encodeScopeJSON(scopes []string) []byte {
	data, _ := json.Marshal(scopes)
	return data
}

// recordDeviceAuthorization writes the device flow's own audit record:
// a user code was approved at the verification endpoint. Best-effort —
// the approval itself has already committed, and a failed log must not
// fail the flow the account just completed. The device and user codes
// never ride the record.
func (s *Service) recordDeviceAuthorization(ctx context.Context, userID, clientID string) {
	s.audit.Record(ctx, s.pool, fwaudit.Entry{
		Event:        audit.EventOidcDeviceAuthorized,
		Status:       fwaudit.StatusSuccess,
		ResourceType: ResourceOidcClient,
		Payload:      map[string]string{"client_id": clientID, "user_id": userID},
	})
}

// decodeScopeJSON reads the stored JSONB onto the scope list. A column
// no write has filled is an empty list.
func decodeScopeJSON(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var scopes []string
	if err := json.Unmarshal(raw, &scopes); err != nil {
		return nil
	}
	return scopes
}
