package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/internal/datastore"
)

// DefaultBucketName is the bucket every storage feature writes into until an
// administrator changes the default selection in the settings.
const DefaultBucketName = "default"

// ErrNoBucket is what Resolve answers for a bucket name that does not exist.
var ErrNoBucket = errors.New("storage: no bucket")

// Bucket is one logical namespace over the configured backend.
type Bucket struct {
	ID               string
	Name             string
	FileSizeLimit    *int64
	AllowedMimeTypes []string
}

// Buckets reads the bucket table. The engine only needs lookups — creation,
// updates, and deletion belong to the bucket management service.
type Buckets struct{}

// NewBuckets builds the bucket repository.
func NewBuckets() *Buckets { return &Buckets{} }

// Resolve reads one bucket by name. ErrNoBucket for a name the table does
// not hold.
func (Buckets) Resolve(ctx context.Context, q datastore.Querier, name string) (Bucket, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "name", "file_size_limit", "allowed_mime_types")
	sb.From(bucketsTable)
	sb.Where(sb.Equal("name", name))

	query, args := sb.Build()
	var b Bucket
	var mimeTypes []string
	err := q.QueryRow(ctx, query, args...).Scan(&b.ID, &b.Name, &b.FileSizeLimit, &mimeTypes)
	if errors.Is(err, datastore.ErrNoRows) {
		return Bucket{}, ErrNoBucket
	}
	if err != nil {
		return Bucket{}, fmt.Errorf("storage: resolve bucket %q: %w", name, err)
	}
	b.AllowedMimeTypes = mimeTypes
	return b, nil
}

// ValidateBucketName checks a bucket name against the one path-segment rule
// a bucket name must satisfy: a lowercase slug of letters, digits, dash, and
// underscore, and not one of the data-directory names the engine reserves
// for itself — those live beside the bucket directories on disk.
func ValidateBucketName(name string) error {
	if err := ValidateKey(name); err != nil {
		return fmt.Errorf("storage: invalid bucket name %q: %w", name, ErrInvalidKey)
	}
	if name != strings.ToLower(name) {
		return fmt.Errorf("storage: bucket name %q must be lowercase", name)
	}
	switch name {
	case "staging", "logs", "backup", "config", "files":
		return fmt.Errorf("storage: bucket name %q is reserved", name)
	}
	return nil
}

// SplitRef splits a bucket-scoped reference (`bucket/key`) into its bucket
// name and key. The first segment is the bucket; everything after it is the
// key. The queue and the staging watcher speak in these composite references
// because the staging tree carries the bucket as its first directory.
func SplitRef(ref string) (bucket, key string, err error) {
	name, rest, found := strings.Cut(ref, "/")
	if !found || name == "" || rest == "" {
		return "", "", fmt.Errorf("storage: invalid bucket-scoped key %q", ref)
	}
	if err := ValidateBucketName(name); err != nil {
		return "", "", err
	}
	if err := ValidateKey(rest); err != nil {
		return "", "", err
	}
	return name, rest, nil
}
