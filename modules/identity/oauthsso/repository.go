package oauthsso

import (
	"context"
	"errors"
	"fmt"
	"time"

	"encoding/json/v2"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/riipandi/tango/internal/datastore"
)

// The failures the repository reports. The service maps them onto the
// errors the handler turns into connect codes.
var (
	// ErrConnectionNotFound is an identifier or slug that names no live
	// connection.
	ErrConnectionNotFound = errors.New("oauthsso: connection not found")

	// ErrProviderTaken is a create or update whose provider slug the
	// unique index already holds — one connection per provider.
	ErrProviderTaken = errors.New("oauthsso: a connection with this provider slug already exists")
)

const connectionColumns = `id, kind, provider, display_name, discovery_url, endpoints,
	client_id, client_secret, scopes, attribute_mapping, enabled, created_at, updated_at`

// Repository reads and writes the connection rows. The linked accounts
// and the flows get their queries beside these as their phases land; the
// connection CRUD is the phase 2 surface.
type Repository struct {
	pool *datastore.Postgres
}

// NewRepository builds the repository over the shared pool.
func NewRepository(pool *datastore.Postgres) *Repository {
	return &Repository{pool: pool}
}

// Insert writes one connection row. The caller has already sealed the
// secret and validated the endpoints; the row's id comes from the
// database's uuidv7 default.
func (r *Repository) Insert(ctx context.Context, db datastore.Querier, conn Connection) (uuid.UUID, time.Time, error) {
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(connectionTable)
	sb.Cols("kind", "provider", "display_name", "discovery_url", "endpoints",
		"client_id", "client_secret", "scopes", "attribute_mapping", "enabled")
	sb.Values(
		string(conn.Kind), conn.Provider, conn.DisplayName, nullIfEmpty(conn.DiscoveryURL),
		endpointsJSON(conn.Endpoints), conn.ClientID, conn.ClientSecret,
		scopesJSON(conn.Scopes), mappingJSON(conn.AttributeMapping), conn.Enabled,
	)
	sb.SQL("RETURNING id, created_at")
	query, args := sb.Build()

	var (
		id     uuid.UUID
		create time.Time
	)
	err := db.QueryRow(ctx, query, args...).Scan(&id, &create)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return uuid.Nil(), time.Time{}, ErrProviderTaken
		}
		return uuid.Nil(), time.Time{}, err
	}
	return id, create, nil
}

// List answers every live connection in provider order — the order the
// operator's screen and the slug-named resolution both read.
func (r *Repository) List(ctx context.Context) ([]Connection, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(connectionColumns)
	sb.From(connectionTable)
	sb.OrderBy("provider")
	query, args := sb.Build()

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Connection
	for rows.Next() {
		conn, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, conn)
	}
	return out, rows.Err()
}

// ByID reads one connection by its row id, on the query surface it is
// handed, so a caller's transaction reads its own view.
func (r *Repository) ByID(ctx context.Context, db datastore.Querier, id uuid.UUID) (Connection, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(connectionColumns)
	sb.From(connectionTable)
	sb.Where(sb.Equal("id", id))
	query, args := sb.Build()
	return scanConnection(db.QueryRow(ctx, query, args...))
}

// ByProvider reads one connection by its provider slug — the word
// BeginSignIn names.
func (r *Repository) ByProvider(ctx context.Context, db datastore.Querier, provider string) (Connection, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(connectionColumns)
	sb.From(connectionTable)
	sb.Where(sb.Equal("provider", provider))
	query, args := sb.Build()
	return scanConnection(db.QueryRow(ctx, query, args...))
}

// Update rewrites one connection's editable columns and answers the row's
// new updated_at. The caller has already sealed whatever secret it is
// replacing and validated whatever endpoints it is storing; the WHERE is
// the id alone, because the connection is not single-use state.
func (r *Repository) Update(ctx context.Context, db datastore.Querier, id uuid.UUID, conn Connection) (time.Time, error) {
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(connectionTable)
	sb.Set(
		sb.Assign("display_name", conn.DisplayName),
		sb.Assign("discovery_url", nullIfEmpty(conn.DiscoveryURL)),
		sb.Assign("endpoints", endpointsJSON(conn.Endpoints)),
		sb.Assign("client_id", conn.ClientID),
		sb.Assign("client_secret", conn.ClientSecret),
		sb.Assign("scopes", scopesJSON(conn.Scopes)),
		sb.Assign("attribute_mapping", mappingJSON(conn.AttributeMapping)),
		sb.Assign("enabled", conn.Enabled),
	)
	sb.Where(sb.Equal("id", id))
	sb.SQL("RETURNING updated_at")
	query, args := sb.Build()

	var updated *time.Time
	err := db.QueryRow(ctx, query, args...).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrConnectionNotFound
	}
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return time.Time{}, ErrProviderTaken
		}
		return time.Time{}, err
	}
	if updated == nil {
		return time.Time{}, nil
	}
	return *updated, nil
}

// Delete removes one connection row. The soft-delete trigger captures the
// removed row into public.deleted_records; the foreign keys carry the
// linked accounts and the live flows with it.
func (r *Repository) Delete(ctx context.Context, db datastore.Querier, id uuid.UUID) (bool, error) {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(connectionTable)
	dbb.Where(dbb.Equal("id", id))
	query, args := dbb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// scanConnection renders one row onto the view. The client secret rides
// exactly as stored — sealed — and every surface read drops it before the
// answer leaves the service.
func scanConnection(row pgx.Row) (Connection, error) {
	var (
		conn      Connection
		endpoints []byte
		scopes    []byte
		mapping   []byte
		discovery *string
	)
	err := row.Scan(&conn.ID, &conn.Kind, &conn.Provider, &conn.DisplayName, &discovery,
		&endpoints, &conn.ClientID, &conn.ClientSecret, &scopes, &mapping,
		&conn.Enabled, &conn.CreatedAt, &conn.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, ErrConnectionNotFound
	}
	if err != nil {
		return Connection{}, err
	}
	if discovery != nil {
		conn.DiscoveryURL = *discovery
	}
	if len(endpoints) > 0 {
		if err := json.Unmarshal(endpoints, &conn.Endpoints); err != nil {
			return Connection{}, fmt.Errorf("oauthsso: stored endpoints: %w", err)
		}
	}
	if len(scopes) > 0 {
		if err := json.Unmarshal(scopes, &conn.Scopes); err != nil {
			return Connection{}, fmt.Errorf("oauthsso: stored scopes: %w", err)
		}
	}
	if len(mapping) > 0 {
		if err := json.Unmarshal(mapping, &conn.AttributeMapping); err != nil {
			return Connection{}, fmt.Errorf("oauthsso: stored attribute mapping: %w", err)
		}
	}
	return conn, nil
}

// nullIfEmpty renders an optional text column: an empty Go string is the
// column's NULL, never a stored empty value.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// endpointsJSON renders the endpoint set for its JSONB column. A zero
// struct — a builtin connection, whose endpoints are the code's own — is
// the column's NULL.
func endpointsJSON(e Endpoints) any {
	if e.Authorization == "" && e.Token == "" && e.Userinfo == "" && e.Jwks == "" {
		return nil
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	return raw
}

// scopesJSON renders the scope list for its JSONB column.
func scopesJSON(scopes []string) any {
	if scopes == nil {
		return []byte(`[]`)
	}
	raw, err := json.Marshal(scopes)
	if err != nil {
		return []byte(`[]`)
	}
	return raw
}

// mappingJSON renders the attribute mapping for its JSONB column.
func mappingJSON(m AttributeMapping) any {
	raw, err := json.Marshal(m)
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}
