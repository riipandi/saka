package scimsync

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/datastore"
)

// ErrNoProvider reports a client that names no provisioning target, or an
// id that names no row. The caller refuses it as the not-found it is.
var ErrNoProvider = errors.New("scimsync: no such service provider")

// ErrProviderExists reports a client that already hangs a provider on
// itself. One provider per client: the provider IS the client's outbound
// provisioning target, and a second row for the same client is the defect
// the schema's unique index refuses anyway.
var ErrProviderExists = errors.New("scimsync: the client already has a service provider")

// Repository reads and writes the provider rows.
type Repository struct{}

// NewRepository builds the repository. It holds no connection: every method
// takes the query surface, so a caller owns the transaction.
func NewRepository() *Repository { return &Repository{} }

// Columns are the columns every read answers, in scan order.
var providerColumns = []string{"id", "oidc_client_id", "endpoint", "token", "created_at", "last_synced_at"}

// Create seals nothing — the caller seals the token — and inserts one row.
// The unique index on oidc_client_id turns a second provider for one
// client into a refused insert.
func (r *Repository) Create(ctx context.Context, db datastore.Querier, p Provider) (Provider, error) {
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(entity.TableScimServiceProviders)
	sb.Cols("id", "oidc_client_id", "endpoint", "token")
	sb.Values(p.ID, p.ClientID, p.Endpoint, p.SealedToken)
	sb.SQL("RETURNING " + joinCols(providerColumns))
	query, args := sb.Build()

	row := db.QueryRow(ctx, query, args...)
	created, err := scanProvider(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Provider{}, ErrProviderExists
		}
		return Provider{}, fmt.Errorf("scimsync: create provider: %w", err)
	}
	return created, nil
}

// isUniqueViolation reports a refused insert on a unique index.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ByID answers one row.
func (r *Repository) ByID(ctx context.Context, db datastore.Querier, id uuid.UUID) (Provider, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(providerColumns...)
	sb.From(entity.TableScimServiceProviders)
	sb.Where(sb.Equal("id", id))
	query, args := sb.Build()

	row := db.QueryRow(ctx, query, args...)
	provider, err := scanProvider(row)
	if errors.Is(err, datastore.ErrNoRows) {
		return Provider{}, ErrNoProvider
	}
	if err != nil {
		return Provider{}, fmt.Errorf("scimsync: read provider: %w", err)
	}
	return provider, nil
}

// ByClient answers the row one client syncs to. The unique index makes the
// answer at most one.
func (r *Repository) ByClient(ctx context.Context, db datastore.Querier, clientID string) (Provider, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(providerColumns...)
	sb.From(entity.TableScimServiceProviders)
	sb.Where(sb.Equal("oidc_client_id", clientID))
	query, args := sb.Build()

	row := db.QueryRow(ctx, query, args...)
	provider, err := scanProvider(row)
	if errors.Is(err, datastore.ErrNoRows) {
		return Provider{}, ErrNoProvider
	}
	if err != nil {
		return Provider{}, fmt.Errorf("scimsync: read provider by client: %w", err)
	}
	return provider, nil
}

// Update replaces a row's endpoint and sealed token, and answers the new
// row. Zero rows affected is the not-found.
func (r *Repository) Update(ctx context.Context, db datastore.Querier, p Provider) (Provider, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableScimServiceProviders)
	ub.Set(
		ub.Assign("endpoint", p.Endpoint),
		ub.Assign("token", p.SealedToken),
	)
	ub.Where(ub.Equal("id", p.ID))
	query, args := ub.Build()

	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return Provider{}, fmt.Errorf("scimsync: update provider: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Provider{}, ErrNoProvider
	}
	return p, nil
}

// Delete removes one row. Zero rows affected is the not-found.
func (r *Repository) Delete(ctx context.Context, db datastore.Querier, id uuid.UUID) error {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(entity.TableScimServiceProviders)
	dbb.Where(dbb.Equal("id", id))
	query, args := dbb.Build()

	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("scimsync: delete provider: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNoProvider
	}
	return nil
}

// StampSynced records a completed pass. The single-use state rides the
// UPDATE's WHERE, so a row deleted mid-pass does not come back to life
// with a timestamp.
func (r *Repository) StampSynced(ctx context.Context, db datastore.Querier, id uuid.UUID, at time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableScimServiceProviders)
	ub.Set(ub.Assign("last_synced_at", at))
	ub.Where(ub.Equal("id", id))
	query, args := ub.Build()

	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("scimsync: stamp synced: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNoProvider
	}
	return nil
}

// All answers every provider row, ordered deterministically for the
// recurring pass.
func (r *Repository) All(ctx context.Context, db datastore.Querier) ([]Provider, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(providerColumns...)
	sb.From(entity.TableScimServiceProviders)
	sb.OrderBy("id")
	query, args := sb.Build()

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("scimsync: list providers: %w", err)
	}
	defer rows.Close()

	var providers []Provider
	for rows.Next() {
		provider, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scimsync: list providers: %w", err)
	}
	return providers, nil
}

// scanner is the subset of pgx rows a scan consumes; a QueryRow and a
// Rows both satisfy it.
type scanner interface {
	Scan(dest ...any) error
}

func scanProvider(row scanner) (Provider, error) {
	var p Provider
	err := row.Scan(&p.ID, &p.ClientID, &p.Endpoint, &p.SealedToken, &p.CreatedAt, &p.LastSyncedAt)
	if errors.Is(err, datastore.ErrNoRows) {
		return Provider{}, err
	}
	if err != nil {
		return Provider{}, err
	}
	return p, nil
}

func joinCols(cols []string) string {
	out := ""
	for i, c := range cols {
		if i > 0 {
			out += ", "
		}
		out += c
	}
	return out
}
