package oidc

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"
	"uuid"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/identity/usergroup"
)

// wireGroupID renders a group row's UUID in the wire form the group surface
// answers with, through the one converter the group feature owns.
func wireGroupID(raw uuid.UUID) string {
	return usergroup.FormatID(raw)
}

// Repository reads and writes the client rows the procedures manage. Every
// method takes the query surface, so the service passes either the pool or
// the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository.
func NewRepository() *Repository {
	return &Repository{}
}

// clientColumns are the columns the client procedures read, in scan order,
// spelled for the queries that name one table.
var clientColumns = []string{
	"id", "name", "description", "callback_urls", "logout_callback_urls",
	"launch_url", "credentials", "is_public", "pkce_enabled", "pkce_supported",
	"requires_reauthentication", "requires_pushed_authorization_requests",
	"skip_consent", "is_group_restricted", "client_type", "logo_path",
	"access_token_duration_minutes", "refresh_token_duration_minutes",
	"metadata_expires_at", "metadata_grant_types",
	"backchannel_logout_uri", "backchannel_logout_session_required",
	"allowed_grant_types", "created_by_id", "created_at",
}

// clientSortColumns is the whitelist a list's sort key resolves through. The
// names are the wire values the list requests validate against; the name
// columns sort case-insensitively, the way the other surfaces' do.
var clientSortColumns = map[string]string{
	"id":         "id",
	"name":       "lower(name)",
	"created_at": "created_at",
}

// scanClient reads one row into the schema. The JSONB columns arrive as raw
// bytes and are rendered onto their values here, so a caller never sees the
// wire form of a list or a document.
func scanClient(scan func(dest ...any) error) (ClientSchema, error) {
	var row ClientSchema
	var callbacks, logoutCallbacks, metadataGrants, allowedGrants []byte
	err := scan(
		&row.ID, &row.Name, &row.Description, &callbacks, &logoutCallbacks,
		&row.LaunchURL, &row.Credentials, &row.IsPublic, &row.PkceEnabled,
		&row.PkceSupported, &row.RequiresReauthentication,
		&row.RequiresPushedAuthorizationRequests, &row.SkipConsent,
		&row.IsGroupRestricted, &row.ClientType, &row.LogoPath,
		&row.AccessTokenDurationMinutes, &row.RefreshTokenDurationMinutes,
		&row.MetadataExpiresAt, &metadataGrants,
		&row.BackchannelLogoutURI, &row.BackchannelLogoutSessionRequired,
		&allowedGrants, &row.CreatedByID, &row.CreatedAt,
	)
	if err != nil {
		return ClientSchema{}, err
	}
	row.CallbackURLs = stringList(callbacks)
	row.LogoutCallbackURLs = stringList(logoutCallbacks)
	row.MetadataGrantTypes = stringList(metadataGrants)
	row.AllowedGrantTypes = stringList(allowedGrants)
	return row, nil
}

// stringList renders one JSONB string array. A column no write has filled —
// SQL `null` — is an empty list, not a failure.
func stringList(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var parsed []string
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return []string{}
	}
	return parsed
}

// jsonText renders a Go value into the text form a jsonb parameter binds
// from: pgx encodes a string parameter into the column's jsonb type, and a
// []byte would arrive as bytea — a type jsonb does not accept implicitly.
func jsonText(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("oidc: encode jsonb: %w", err)
	}
	return string(raw), nil
}

// GetClient reads one client by its identifier. An identifier that names no
// client is the caller's not-found failure.
func (r *Repository) GetClient(ctx context.Context, db datastore.Querier, id string) (ClientSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(clientColumns...)
	sb.From(ClientTable)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	row, err := scanClient(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return ClientSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return ClientSchema{}, fmt.Errorf("oidc: get client: %w", err)
	}
	return row, nil
}

// GetClientForUpdate reads one client under the row lock, the read a
// read-modify-write opens with: the rewrite, the restriction, and the
// secrets document all hold the row between their read and their write, so
// two procedures racing hold one state between them.
func (r *Repository) GetClientForUpdate(ctx context.Context, db datastore.Querier, id string) (ClientSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(clientColumns...)
	sb.From(ClientTable)
	sb.Where(sb.Equal("id", id))
	sb.ForUpdate()

	query, args := sb.Build()
	row, err := scanClient(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return ClientSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return ClientSchema{}, fmt.Errorf("oidc: lock client: %w", err)
	}
	return row, nil
}

// ListClients answers one page of the clients. A search term narrows by
// name, case-insensitively.
func (r *Repository) ListClients(ctx context.Context, db datastore.Querier, search, sortBy string, ascending bool, offset, limit int) ([]ClientSchema, int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(clientColumns...)
	sb.From(ClientTable)
	if search != "" {
		sb.Where(sb.ILike("name", "%"+search+"%"))
	}
	sb.OrderBy(datastore.ListOrder(clientSortColumns, sortBy, "created_at", ascending), "id")
	sb.Limit(limit).Offset(offset)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("oidc: list clients: %w", err)
	}
	defer rows.Close()

	clients := []ClientSchema{}
	for rows.Next() {
		row, err := scanClient(rows.Scan)
		if err != nil {
			return nil, 0, fmt.Errorf("oidc: list clients: %w", err)
		}
		clients = append(clients, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("oidc: list clients: %w", err)
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(ClientTable)
	if search != "" {
		cb.Where(cb.ILike("name", "%"+search+"%"))
	}
	query, args = cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("oidc: count clients: %w", err)
	}
	return clients, total, nil
}

// CreateClient stores the client row. The unique primary key is the storage
// of the identifier rule, and the service reads the write's failure to
// answer a duplicate.
func (r *Repository) CreateClient(ctx context.Context, db datastore.Querier, row ClientSchema) error {
	callbacks, err := jsonText(row.CallbackURLs)
	if err != nil {
		return err
	}
	logoutCallbacks, err := jsonText(row.LogoutCallbackURLs)
	if err != nil {
		return err
	}
	credentials, err := jsonText(readCredentials(row.Credentials))
	if err != nil {
		return err
	}
	metadataGrants, err := jsonText(row.MetadataGrantTypes)
	if err != nil {
		return err
	}
	allowedGrants, err := jsonText(row.AllowedGrantTypes)
	if err != nil {
		return err
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(ClientTable)
	ib.Cols(
		"id", "name", "description", "callback_urls", "logout_callback_urls",
		"launch_url", "credentials", "is_public", "pkce_enabled", "pkce_supported",
		"requires_reauthentication", "requires_pushed_authorization_requests",
		"skip_consent", "is_group_restricted", "client_type",
		"access_token_duration_minutes", "refresh_token_duration_minutes",
		"metadata_expires_at", "metadata_grant_types",
		"backchannel_logout_uri", "backchannel_logout_session_required",
		"allowed_grant_types", "created_by_id",
	)
	ib.Values(
		row.ID, row.Name, row.Description, callbacks, logoutCallbacks,
		row.LaunchURL, credentials, row.IsPublic, row.PkceEnabled,
		row.PkceSupported, row.RequiresReauthentication,
		row.RequiresPushedAuthorizationRequests, row.SkipConsent,
		row.IsGroupRestricted, row.ClientType,
		row.AccessTokenDurationMinutes, row.RefreshTokenDurationMinutes,
		row.MetadataExpiresAt, metadataGrants,
		row.BackchannelLogoutURI, row.BackchannelLogoutSessionRequired,
		allowedGrants, row.CreatedByID,
	)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("oidc: create client: %w", err)
	}
	return nil
}

// UpdateCIMDClient rewrites the fields a metadata document names — the
// display name, the redirect URIs, the declared grants, the refresh
// deadline. The secrets, the logo, the restriction, and the token windows
// are not the document's to change.
func (r *Repository) UpdateCIMDClient(ctx context.Context, db datastore.Querier, row ClientSchema) (bool, error) {
	callbacks, err := jsonText(row.CallbackURLs)
	if err != nil {
		return false, err
	}
	logoutCallbacks, err := jsonText(row.LogoutCallbackURLs)
	if err != nil {
		return false, err
	}
	metadataGrants, err := jsonText(row.MetadataGrantTypes)
	if err != nil {
		return false, err
	}

	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(ClientTable)
	ub.Set(
		ub.Assign("name", row.Name),
		ub.Assign("callback_urls", callbacks),
		ub.Assign("logout_callback_urls", logoutCallbacks),
		ub.Assign("metadata_expires_at", row.MetadataExpiresAt),
		ub.Assign("metadata_grant_types", metadataGrants),
	)
	ub.Where(ub.Equal("id", row.ID), ub.Equal("client_type", ClientTypeCIMD))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("oidc: update cimd client: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// UpdateClient replaces a client's fields. The secrets, the logo, and the
// group restriction are not fields of this write: they have their own
// procedures, because a rotation and an access change are happenings of
// their own. The WHERE repeats the identifier, so a client deleted between
// the read and the write answers unchanged.
func (r *Repository) UpdateClient(ctx context.Context, db datastore.Querier, row ClientSchema) (bool, error) {
	callbacks, err := jsonText(row.CallbackURLs)
	if err != nil {
		return false, err
	}
	logoutCallbacks, err := jsonText(row.LogoutCallbackURLs)
	if err != nil {
		return false, err
	}
	allowedGrants, err := jsonText(row.AllowedGrantTypes)
	if err != nil {
		return false, err
	}

	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(ClientTable)
	ub.Set(
		ub.Assign("name", row.Name),
		ub.Assign("description", row.Description),
		ub.Assign("callback_urls", callbacks),
		ub.Assign("logout_callback_urls", logoutCallbacks),
		ub.Assign("launch_url", row.LaunchURL),
		ub.Assign("is_public", row.IsPublic),
		ub.Assign("pkce_enabled", row.PkceEnabled),
		ub.Assign("pkce_supported", row.PkceSupported),
		ub.Assign("requires_reauthentication", row.RequiresReauthentication),
		ub.Assign("requires_pushed_authorization_requests", row.RequiresPushedAuthorizationRequests),
		ub.Assign("skip_consent", row.SkipConsent),
		ub.Assign("is_group_restricted", row.IsGroupRestricted),
		ub.Assign("access_token_duration_minutes", row.AccessTokenDurationMinutes),
		ub.Assign("refresh_token_duration_minutes", row.RefreshTokenDurationMinutes),
		ub.Assign("backchannel_logout_uri", row.BackchannelLogoutURI),
		ub.Assign("backchannel_logout_session_required", row.BackchannelLogoutSessionRequired),
		ub.Assign("allowed_grant_types", allowedGrants),
	)
	ub.Where(ub.Equal("id", row.ID))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("oidc: update client: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteClient removes one client. The codes, sessions, grants, and group
// restrictions that name it die with it, by the schema's cascades.
func (r *Repository) DeleteClient(ctx context.Context, db datastore.Querier, id string) (bool, error) {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(ClientTable)
	dbb.Where(dbb.Equal("id", id))

	query, args := dbb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("oidc: delete client: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetLogoPath records the key the client's logo lives under, or clears it.
// The WHERE repeats the identifier, so a client deleted between the read and
// the write answers unchanged.
func (r *Repository) SetLogoPath(ctx context.Context, db datastore.Querier, id string, path *string) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(ClientTable)
	ub.Set(ub.Assign("logo_path", path))
	ub.Where(ub.Equal("id", id))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("oidc: set logo path: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// writeCredentials replaces the client's secrets document. The read-modify-
// write runs in the caller's transaction under the row lock the caller took,
// so two secret procedures racing hold one document between them.
func (r *Repository) writeCredentials(ctx context.Context, db datastore.Querier, id string, stored credentials) error {
	document, err := jsonText(stored)
	if err != nil {
		return err
	}

	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(ClientTable)
	ub.Set(ub.Assign("credentials", document))
	ub.Where(ub.Equal("id", id))

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("oidc: write credentials: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return datastore.ErrNoRows
	}
	return nil
}

// SetAllowedGroups replaces the group restriction's set. The delete-then-
// insert runs in the caller's transaction, so the replacement is whole or
// nothing; a foreign key the junction carries turns an unknown group into
// the refusal the service maps, keeping the set it held.
func (r *Repository) SetAllowedGroups(ctx context.Context, db datastore.Querier, clientID string, ids []uuid.UUID) error {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(AllowedGroupsTable)
	dbb.Where(dbb.Equal("oidc_client_id", clientID))
	query, args := dbb.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("oidc: clear allowed groups: %w", err)
	}

	if len(ids) == 0 {
		return nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(AllowedGroupsTable)
	ib.Cols("oidc_client_id", "user_group_id")
	for _, id := range ids {
		ib.Values(clientID, id)
	}
	query, args = ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrGroupUnknown
		}
		return fmt.Errorf("oidc: set allowed groups: %w", err)
	}
	return nil
}

// ListAllowedGroups answers the groups the restriction names, ordered by
// display name. The junction is this feature's table; the group columns it
// joins are read beside it, the way the account view reads its memberships.
func (r *Repository) ListAllowedGroups(ctx context.Context, db datastore.Querier, clientID string) ([]GroupRef, error) {
	byClient, err := r.GroupsOfClients(ctx, db, []string{clientID})
	if err != nil {
		return nil, err
	}
	// A client with no restriction answered an empty roll before the batch
	// existed, and the map's miss answers nil — the caller reads a roll.
	if groups := byClient[clientID]; groups != nil {
		return groups, nil
	}
	return []GroupRef{}, nil
}

// GroupsOfClients answers the allowed groups of every named client in one
// read, grouped by the client id in wire form, each slice ordered by the
// group's display name. The client list page is the caller that pays for the
// batch: its one answer carries every client's restriction, and a per-row
// read there would pay the junction join once per client on the page.
func (r *Repository) GroupsOfClients(ctx context.Context, db datastore.Querier, clientIDs []string) (map[string][]GroupRef, error) {
	grouped := make(map[string][]GroupRef, len(clientIDs))
	if len(clientIDs) == 0 {
		return grouped, nil
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("j.oidc_client_id", "g.id", "g.name", "g.display_name")
	sb.From(AllowedGroupsTable + " j")
	sb.JoinWithOption(sqlbuilder.InnerJoin, "public.user_groups g", "g.id = j.user_group_id")
	sb.Where(sb.In("j.oidc_client_id", sqlbuilder.List(clientIDs)))
	sb.OrderBy("j.oidc_client_id", "lower(g.display_name)", "g.id")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("oidc: list allowed groups: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var clientID, rawID, name, displayName string
		if err := rows.Scan(&clientID, &rawID, &name, &displayName); err != nil {
			return nil, fmt.Errorf("oidc: list allowed groups: %w", err)
		}
		id, err := uuid.Parse(rawID)
		if err != nil {
			return nil, fmt.Errorf("oidc: list allowed groups: %w", err)
		}
		grouped[clientID] = append(grouped[clientID], GroupRef{
			ID:          wireGroupID(id),
			Name:        name,
			DisplayName: displayName,
		})
	}
	return grouped, rows.Err()
}
