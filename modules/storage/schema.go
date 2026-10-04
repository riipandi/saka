package storage

import (
	"time"

	"uuid"

	"go.jetify.com/typeid"

	"github.com/riipandi/saka/pkg/strutils"
)

// ResourceBucket is the resource type an audit record names when the change
// is about a bucket.
const ResourceBucket = "storage_bucket"

// BucketIDPrefix is the TypeID prefix of a bucket's identifier. The id
// leaves the server in an API response, so the reader of a log line or a
// support ticket can tell what it names without a lookup.
type BucketIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (BucketIDPrefix) Prefix() string { return "bkt" }

// BucketID is the typed identifier of one row of entity.TableStorageBuckets, in its wire
// form. The column stays a UUID; the conversion is strutils', bound to this
// prefix by the type.
type BucketID = typeid.TypeID[BucketIDPrefix]

// IDFromUUID wraps the row's UUID into the wire form.
func IDFromUUID(raw uuid.UUID) (BucketID, error) {
	return strutils.EncodeID[BucketID](raw)
}

// FormatID renders the wire form of a row's UUID. Rows read from the
// database always carry a valid UUID, so the render cannot fail; an invalid
// one answers the empty string, which no consumer should mistake for an id.
func FormatID(raw uuid.UUID) string {
	return strutils.FormatID[BucketID](raw)
}

// UUIDFromWire reads the row's UUID out of the wire form. A malformed
// identifier names no bucket — the caller refuses it as the not-found it
// is.
func UUIDFromWire(wire string) (uuid.UUID, error) {
	return strutils.UUIDFromWire[BucketID](wire)
}

// BucketSchema is one row of entity.TableStorageBuckets. The nullable limit columns are
// nullable by construction: NULL is unlimited in both, and the trigger
// fills updated_at on the first update.
type BucketSchema struct {
	ID               uuid.UUID `db:"id"`
	Name             string    `db:"name"`
	FileSizeLimit    *int64    `db:"file_size_limit"`
	AllowedMimeTypes []string  `db:"allowed_mime_types"`
	CreatedAt        time.Time `db:"created_at"`
	// UpdatedAt carries the column's insert default on a fresh row and the
	// trigger's stamp after the first update: the column is NOT NULL, so it
	// always answers an instant.
	UpdatedAt time.Time `db:"updated_at"`
}
