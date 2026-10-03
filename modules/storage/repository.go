package storage

import (
	"context"
	"errors"
	"fmt"

	"uuid"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/internal/datastore"
)

// Repository reads and writes the bucket rows the management procedures
// serve. Every method takes the query surface, so the service passes either
// the pool or the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository.
func NewRepository() *Repository { return &Repository{} }

// bucketColumns are the columns the bucket procedures read, in scan order.
var bucketColumns = []string{
	"id", "name", "file_size_limit", "allowed_mime_types", "created_at", "updated_at",
}

// scanBucket reads one row into the schema.
func scanBucket(scan func(dest ...any) error) (BucketSchema, error) {
	var row BucketSchema
	err := scan(&row.ID, &row.Name, &row.FileSizeLimit, &row.AllowedMimeTypes, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return BucketSchema{}, err
	}
	return row, nil
}

// Insert writes one bucket row and answers its identifier.
func (r *Repository) Insert(ctx context.Context, db datastore.Querier, name string, sizeLimit *int64, mimeTypes []string) (uuid.UUID, error) {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(BucketTable)
	ib.Cols("id", "name", "file_size_limit", "allowed_mime_types")
	ib.Values(uuid.NewV7(), name, sizeLimit, mimeTypes)
	ib.SQL("RETURNING id")

	query, args := ib.Build()
	var id uuid.UUID
	if err := db.QueryRow(ctx, query, args...).Scan(&id); err != nil {
		return uuid.Nil(), err
	}
	return id, nil
}

// GetByName reads one bucket by its name. An unknown name is the caller's
// not-found failure.
func (r *Repository) GetByName(ctx context.Context, db datastore.Querier, name string) (BucketSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(bucketColumns...)
	sb.From(BucketTable)
	sb.Where(sb.Equal("name", name))

	query, args := sb.Build()
	row, err := scanBucket(func(dest ...any) error {
		return db.QueryRow(ctx, query, args...).Scan(dest...)
	})
	if errors.Is(err, datastore.ErrNoRows) {
		return BucketSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return BucketSchema{}, fmt.Errorf("storage: get bucket %q: %w", name, err)
	}
	return row, nil
}

// List answers every bucket, oldest first. The set is small and
// administrator-managed, so no pagination exists here.
func (r *Repository) List(ctx context.Context, db datastore.Querier) ([]BucketSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(bucketColumns...)
	sb.From(BucketTable)
	sb.OrderBy("created_at")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list buckets: %w", err)
	}
	defer rows.Close()

	buckets := []BucketSchema{}
	for rows.Next() {
		row, scanErr := scanBucket(rows.Scan)
		if scanErr != nil {
			return nil, fmt.Errorf("storage: list buckets: %w", scanErr)
		}
		buckets = append(buckets, row)
	}
	return buckets, rows.Err()
}

// Update rewrites one bucket's limits, leaving a NULL argument's column at
// its stored value. The write answers whether a row matched.
func (r *Repository) Update(ctx context.Context, db datastore.Querier, name string, sizeLimit *int64, mimeTypes *[]string) (bool, error) {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(BucketTable)
	assignments := []string{}
	if sizeLimit != nil {
		assignments = append(assignments, ub.Assign("file_size_limit", *sizeLimit))
	}
	if mimeTypes != nil {
		assignments = append(assignments, ub.Assign("allowed_mime_types", *mimeTypes))
	}
	ub.Set(assignments...)
	ub.Where(ub.Equal("name", name))
	ub.SQL("RETURNING 1")

	query, args := ub.Build()
	var one int
	err := db.QueryRow(ctx, query, args...).Scan(&one)
	if errors.Is(err, datastore.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: update bucket %q: %w", name, err)
	}
	return true, nil
}

// Delete removes one bucket row by name. The database's foreign key holds
// the emptiness rule as a backstop — the service reads the count first so
// the refusal is a clean precondition, not a constraint error — and answers
// whether a row was removed.
func (r *Repository) Delete(ctx context.Context, db datastore.Querier, name string) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	sb.DeleteFrom(BucketTable)
	sb.Where(sb.Equal("name", name))

	query, args := sb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("storage: delete bucket %q: %w", name, err)
	}
	return tag.RowsAffected() > 0, nil
}

// CountObjects answers how many object rows — finals and staging intents
// alike — the bucket holds. Any row means the deletion refuses.
func (r *Repository) CountObjects(ctx context.Context, db datastore.Querier, bucketID uuid.UUID) (int64, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(ObjectTable)
	sb.Where(sb.Equal("bucket_id", bucketID))

	query, args := sb.Build()
	var count int64
	if err := db.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("storage: count objects: %w", err)
	}
	return count, nil
}
