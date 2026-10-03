package blocklist

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"uuid"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/datastore"
)

// Repository reads and writes the entry rows the procedures manage. Every
// method takes the query surface, so the service passes either the pool or
// the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository over the shared pool.
func NewRepository() *Repository {
	return &Repository{}
}

// entryColumns are the columns the entry procedures read, in scan order,
// spelled for the query that names one table.
var entryColumns = []string{"id", "pattern", "created_by", "created_at"}

// scanEntry reads one row into the schema.
func scanEntry(scan func(dest ...any) error) (EntrySchema, error) {
	var row EntrySchema
	err := scan(&row.ID, &row.Pattern, &row.CreatedBy, &row.CreatedAt)
	if err != nil {
		return EntrySchema{}, err
	}
	return row, nil
}

// GetEntry reads one entry by its identifier. An identifier that names no
// entry is the caller's not-found failure.
func (r *Repository) GetEntry(ctx context.Context, db datastore.Querier, id uuid.UUID) (EntrySchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(entryColumns...)
	sb.From(entity.TableBlocklistEntries)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	row, err := scanEntry(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return EntrySchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return EntrySchema{}, fmt.Errorf("blocklist: get: %w", err)
	}
	return row, nil
}

// GetEntryByPattern reads one entry by its normalized pattern — the read
// back an idempotent add answers with.
func (r *Repository) GetEntryByPattern(ctx context.Context, db datastore.Querier, pattern string) (EntrySchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(entryColumns...)
	sb.From(entity.TableBlocklistEntries)
	sb.Where(sb.Equal("pattern", pattern))

	query, args := sb.Build()
	row, err := scanEntry(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return EntrySchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return EntrySchema{}, fmt.Errorf("blocklist: get by pattern: %w", err)
	}
	return row, nil
}

// entrySortColumns is the whitelist a list's sort key resolves through. The
// names are the wire values the list request validates against.
var entrySortColumns = map[string]string{
	"pattern":    "pattern",
	"created_at": "created_at",
}

// ListEntries answers one page of the entries, newest first unless the
// caller's sort says otherwise.
func (r *Repository) ListEntries(ctx context.Context, db datastore.Querier, sortBy string, ascending bool, offset, limit int) ([]EntrySchema, int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(entryColumns...)
	sb.From(entity.TableBlocklistEntries)
	sb.OrderBy(datastore.ListOrder(entrySortColumns, sortBy, "created_at", ascending), "id")
	sb.Limit(limit).Offset(offset)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("blocklist: list: %w", err)
	}
	defer rows.Close()

	entries := []EntrySchema{}
	for rows.Next() {
		row, scanErr := scanEntry(rows.Scan)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("blocklist: list: %w", scanErr)
		}
		entries = append(entries, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("blocklist: list: %w", err)
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(entity.TableBlocklistEntries)
	query, args = cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("blocklist: count: %w", err)
	}
	return entries, total, nil
}

// InsertEntry stores one entry and answers whether the row is new: an entry
// the unique index already holds is not an error — the idempotent add — so
// the caller learns the fact and records the audit event only when the row
// is one.
func (r *Repository) InsertEntry(ctx context.Context, db datastore.Querier, row EntrySchema) (bool, error) {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableBlocklistEntries)
	ib.Cols("id", "pattern", "created_by")
	ib.Values(row.ID, row.Pattern, row.CreatedBy)
	ib.SQL("ON CONFLICT (pattern) DO NOTHING")

	query, args := ib.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("blocklist: insert: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteEntry removes one entry. A concurrent removal matches nothing and
// answers false — the state the entry is already in.
func (r *Repository) DeleteEntry(ctx context.Context, db datastore.Querier, id uuid.UUID) (bool, error) {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(entity.TableBlocklistEntries)
	dbb.Where(dbb.Equal("id", id))

	query, args := dbb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("blocklist: delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// EmailsAtDomain answers the lowercased addresses the accounts hold at one
// domain. The collision scan compares bases in Go — the folding is the
// helper's, not SQL's — and the call rate is operator-facing, so the domain
// scan's bound is the deployment's own account count, not the internet's.
func (r *Repository) EmailsAtDomain(ctx context.Context, db datastore.Querier, domain string) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("lower(email)")
	sb.From(entity.TableUsers)
	sb.Where(sb.Like("lower(email)", "%@"+domain))

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("blocklist: emails at domain: %w", err)
	}
	defer rows.Close()

	emails := []string{}
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, fmt.Errorf("blocklist: emails at domain: %w", err)
		}
		emails = append(emails, email)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("blocklist: emails at domain: %w", err)
	}
	return emails, nil
}

// Patterns answers every stored pattern. The gate reads the whole list: the
// blocklist is an administrator's hand-curated set, small by construction,
// and the match is in Go where the subaddress base lives.
func (r *Repository) Patterns(ctx context.Context, db datastore.Querier) ([]string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("pattern")
	sb.From(entity.TableBlocklistEntries)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("blocklist: patterns: %w", err)
	}
	defer rows.Close()

	patterns := []string{}
	for rows.Next() {
		var pattern string
		if err := rows.Scan(&pattern); err != nil {
			return nil, fmt.Errorf("blocklist: patterns: %w", err)
		}
		patterns = append(patterns, strings.ToLower(pattern))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("blocklist: patterns: %w", err)
	}
	return patterns, nil
}
