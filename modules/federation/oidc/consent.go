package oidc

import (
	"context"
	"errors"
	"time"

	sqlbuilder "github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/modules/identity/user"
)

// The consent surface reads and undoes what the authorization flow records
// in user_authorized_oidc_clients. The protocol's half of the ledger —
// reading a scope list, recording an approval — lives in protocol_consent.go;
// this file is the account-facing procedures beside it.

// AuthorizedClientView is one ledger row as the self-service procedures
// answer it: the client, the scopes the consent covers, and the last time
// a grant rode it.
type AuthorizedClientView struct {
	Client     ClientView
	Scopes     []string
	LastUsedAt *time.Time
}

// LedgerEntry is one ledger row with the account named, the shape the
// deployment-wide read answers.
type LedgerEntry struct {
	UserWire   string
	Client     ClientView
	Scopes     []string
	LastUsedAt *time.Time
}

// ledgerClientColumns are the client columns the ledger's joins select,
// each qualified by the client table's alias. The order mirrors
// clientColumns — scanClient's dest order — so the scan helper stays one.
var ledgerClientColumns = []string{
	"c.id", "c.name", "c.description", "c.callback_urls", "c.logout_callback_urls",
	"c.launch_url", "c.credentials", "c.is_public", "c.pkce_enabled", "c.pkce_supported",
	"c.requires_reauthentication", "c.requires_pushed_authorization_requests", "c.skip_consent",
	"c.is_group_restricted", "c.client_type", "c.logo_path",
	"c.access_token_duration_minutes", "c.refresh_token_duration_minutes",
	"c.metadata_expires_at", "c.metadata_grant_types",
	"c.backchannel_logout_uri", "c.backchannel_logout_session_required",
	"c.allowed_grant_types", "c.created_by_id", "c.created_at",
}

// scanLedgerRow reads one ledger join row. The client's columns land in
// scanClient's usual order, the scope document and the last-use instant
// ride behind them.
func scanLedgerRow(scan func(dest ...any) error) (ClientSchema, []string, *time.Time, error) {
	var scopeRaw []byte
	var lastUsed *time.Time
	row, err := scanClient(func(dest ...any) error {
		return scan(append(dest, &scopeRaw, &lastUsed)...)
	})
	if err != nil {
		return ClientSchema{}, nil, nil, err
	}
	return row, decodeScopeJSON(scopeRaw), lastUsed, nil
}

// authorizedClientsFor answers one account's ledger rows, most recently
// used first. A client the ledger names but the clients table has dropped
// cannot appear — the ledger's foreign key removes the row with the
// client.
func (s *Service) authorizedClientsFor(ctx context.Context, db datastore.Querier, userID string) ([]AuthorizedClientView, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(append(ledgerClientColumns, "l.scope", "l.last_used_at")...)
	sb.From(entity.TableUserAuthorizedOIDCClients + " l")
	sb.JoinWithOption(sqlbuilder.InnerJoin, entity.TableOIDCClients+" c ON c.id = l.client_id")
	sb.Where(sb.Equal("l.user_id", userID))
	sb.OrderBy("l.last_used_at DESC")
	query, args := sb.Build()

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	views := []AuthorizedClientView{}
	for rows.Next() {
		row, scopes, lastUsed, scanErr := scanLedgerRow(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		views = append(views, AuthorizedClientView{
			Client:     row.view(s.now()),
			Scopes:     scopes,
			LastUsedAt: lastUsed,
		})
	}
	return views, rows.Err()
}

// MyAuthorizedClients answers the calling account's ledger.
func (s *Service) MyAuthorizedClients(ctx context.Context, userID string) ([]AuthorizedClientView, error) {
	return s.authorizedClientsFor(ctx, s.pool, userID)
}

// UserAuthorizedClients answers one account's ledger — the administrative
// read of the same rows the account's own surface answers.
func (s *Service) UserAuthorizedClients(ctx context.Context, userID string) ([]AuthorizedClientView, error) {
	return s.authorizedClientsFor(ctx, s.pool, userID)
}

// AllAuthorizedClients answers every ledger row with its account named.
// A left join keeps the rows whose account is gone — the delete that
// removed it left the consent behind on purpose.
func (s *Service) AllAuthorizedClients(ctx context.Context) ([]LedgerEntry, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(append(ledgerClientColumns, "l.scope", "l.last_used_at", "u.id")...)
	sb.From(entity.TableUserAuthorizedOIDCClients + " l")
	sb.JoinWithOption(sqlbuilder.InnerJoin, entity.TableOIDCClients+" c ON c.id = l.client_id")
	sb.JoinWithOption(sqlbuilder.LeftJoin, entity.TableUsers+" u ON u.id = l.user_id")
	sb.OrderBy("l.last_used_at DESC")
	query, args := sb.Build()

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []LedgerEntry{}
	for rows.Next() {
		var accountID *string
		row, scopes, lastUsed, scanErr := scanLedgerRow(func(dest ...any) error {
			return rows.Scan(append(dest, &accountID)...)
		})
		if scanErr != nil {
			return nil, scanErr
		}
		entry := LedgerEntry{
			Client:     row.view(s.now()),
			Scopes:     scopes,
			LastUsedAt: lastUsed,
		}
		if accountID != nil {
			if wire, err := user.IDFromUUIDString(*accountID); err == nil {
				entry.UserWire = wire.String()
			}
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// MyClients answers the clients the account may authorize. The rule is
// fail-closed: a client is restricted when its flag is set or its
// allowed-groups roll carries rows, and restricted clients appear only for
// accounts in one of those groups — a flag with an empty roll admits
// nobody. The answer is the consent page's catalogue, not the ledger.
func (s *Service) MyClients(ctx context.Context, userID string) ([]ClientView, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(qualifiedClientColumns("c")...)
	sb.From(entity.TableOIDCClients + " c")
	sb.Where(
		sb.Or(
			sb.And(
				"NOT c.is_group_restricted",
				"NOT EXISTS (SELECT 1 FROM public.oidc_clients_allowed_user_groups g "+
					"WHERE g.oidc_client_id = c.id)",
			),
			"EXISTS (SELECT 1 FROM public.oidc_clients_allowed_user_groups g "+
				"JOIN public.user_groups_users m ON m.user_group_id = g.user_group_id "+
				"WHERE g.oidc_client_id = c.id AND m.user_id = "+sb.Var(userID)+")",
		),
	)
	sb.OrderBy("lower(c.name)")
	query, args := sb.Build()

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	views := []ClientView{}
	for rows.Next() {
		row, scanErr := scanClient(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		views = append(views, row.view(s.now()))
	}
	return views, rows.Err()
}

// RevokeMyAuthorizedClient withdraws one account's consent for a client
// and kills what the consent issued: the grants the account made to the
// client, and every code and refresh token riding them. The whole change
// is one transaction — a consent that revokes without its tokens dying is
// a consent the client keeps using.
func (s *Service) RevokeMyAuthorizedClient(ctx context.Context, userID, clientID string) error {
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		removed, err := s.removeAuthorization(ctx, tx, userID, clientID)
		if err != nil || !removed {
			return err
		}
		if err := s.revokeGrants(ctx, tx, userID, clientID); err != nil {
			return err
		}

		s.audit.Record(ctx, tx, fwaudit.Entry{
			Event:        audit.EventOidcConsentRevoked,
			Status:       fwaudit.StatusSuccess,
			ResourceType: ResourceOidcClient,
			// The client id is the operator's word, not a row UUID, so the
			// payload carries it beside the account.
			Payload: map[string]string{"client_id": clientID, "user_id": userID},
		})
		return nil
	})
}

// EndSession withdraws what an RP-initiated logout names: the grants the
// account made to the client and every token riding them. The
// authorized-client ledger survives unless the
// `oidc.end_session_revokes_consent` setting says otherwise — off, the
// client's next sign-in skips consent; on, it asks again. The sessionID
// is the OP session identifier the hint carried, when it named one — the
// back-channel delivery's sid, nothing in this database names.
func (s *Service) EndSession(ctx context.Context, userID, clientID, sessionID string) error {
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		// The switch is read per call, so an operator's change lands on
		// the next logout without a restart. A read error answers off —
		// the conservative side of the switch: the ledger survives, the
		// next sign-in skips consent — and the warning names the miss.
		revokes, err := s.endSessionRevokesConsent(ctx)
		if err != nil {
			s.log.WarnContext(ctx, "oidc: the end-session consent switch is unread; revoking the grants only",
				"error", err)
		}
		if revokes {
			if _, err := s.removeAuthorization(ctx, tx, userID, clientID); err != nil {
				return err
			}
		}
		if err := s.revokeGrants(ctx, tx, userID, clientID); err != nil {
			return err
		}

		s.audit.Record(ctx, tx, fwaudit.Entry{
			Event:        audit.EventOidcSessionEnded,
			Status:       fwaudit.StatusSuccess,
			ResourceType: ResourceOidcClient,
			Payload:      map[string]string{"client_id": clientID, "user_id": userID},
		})
		return nil
	})
	if err != nil {
		return err
	}

	// The delivery rides the committed end — a token the transaction
	// rolled back must never reach the relying party.
	s.dispatchBackchannelLogout(ctx, userID, clientID, sessionID)
	return nil
}

// endSessionRevokesConsent answers the wired source, a nil one off.
func (s *Service) endSessionRevokesConsent(ctx context.Context) (bool, error) {
	if s.consentRevocation == nil {
		return false, nil
	}
	return s.consentRevocation.EndSessionRevokesConsent(ctx)
}

// revokeGrants kills the grants one account holds for one client and every
// code and refresh token riding them. The grant rows carry the subject in
// their document, the pointer rows carry the grant in theirs — one select
// names the grants, one delete takes the pointers, one takes the grants.
func (s *Service) revokeGrants(ctx context.Context, tx datastore.Querier, userID, clientID string) error {
	grants, err := s.grantKeysFor(ctx, tx, userID, clientID)
	if err != nil {
		return err
	}
	if len(grants) > 0 {
		sb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
		sb.DeleteFrom(entity.TableOAuthSessions)
		sb.Where(
			sb.In("request_data->>'grant_id'", toAny(grants)...),
			sb.In("kind", sessionKindAuthCode, sessionKindRefresh),
		)
		query, args := sb.Build()
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return err
		}
	}
	sb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	sb.DeleteFrom(entity.TableOAuthSessions)
	sb.Where(
		sb.Equal("kind", sessionKindGrant),
		sb.Equal("client_id", clientID),
		"request_data->>'sub' = "+sb.Var(userID),
	)
	query, args := sb.Build()
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return err
	}
	return nil
}

// removeAuthorization drops the ledger row, answering whether one was
// there. An unknown client or an ungranted consent is not an error — the
// withdrawal is idempotent, the way a DELETE already answered is.
func (s *Service) removeAuthorization(ctx context.Context, db datastore.Querier, userID, clientID string) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	sb.DeleteFrom(entity.TableUserAuthorizedOIDCClients)
	sb.Where(sb.Equal("user_id", userID), sb.Equal("client_id", clientID))
	query, args := sb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// grantKeysFor names the grants one account holds for one client.
func (s *Service) grantKeysFor(ctx context.Context, db datastore.Querier, userID, clientID string) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key")
	sb.From(entity.TableOAuthSessions)
	sb.Where(
		sb.Equal("kind", sessionKindGrant),
		sb.Equal("client_id", clientID),
		"request_data->>'sub' = "+sb.Var(userID),
	)
	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// qualifiedClientColumns renders the client column list under one alias.
func qualifiedClientColumns(alias string) []string {
	columns := make([]string, 0, len(clientColumns))
	for _, column := range clientColumns {
		columns = append(columns, alias+"."+column)
	}
	return columns
}

// toAny widens a string slice for the builder's variadic select.
func toAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
