package oidc

import (
	"context"
	"errors"
	"time"

	"encoding/json/v2"
	sqlbuilder "github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"
	"slices"
)

// The consent ledger: user_authorized_oidc_clients answers the question
// the authorization flow asks — has this account agreed to this client
// for these scopes already — and records the agreement when it is made.

// authorizedScopes reads the scopes an earlier consent stored.
func (s *Service) authorizedScopes(ctx context.Context, userID, clientID string) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("scope").From("public.user_authorized_oidc_clients").
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
	sb.InsertInto("public.user_authorized_oidc_clients")
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
