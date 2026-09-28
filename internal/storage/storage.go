// Package storage holds the file storage engine and its two backends: the
// local filesystem and an S3-compatible object store.
//
// A file travels once: a request stages it onto the local disk, and the sync
// — the body of a durable queue task — hashes it and stores it whole in the
// backend under the key the feature composed. The content hash (SHA-256 of
// the whole file) is the manifest's one-value "the backend already holds
// these bytes" check, so a replayed sync skips its PUT, and a changed file
// replaces its object whole. The manifest lives in Postgres, the durable
// place a read and a garbage collection start from; the bytes live in the
// backend, the same tree of keys on both drivers.
//
// There is no chunk store. The earlier design cut a file into
// content-addressed chunks the backend held individually, buying cross-file
// dedup and per-chunk resume at the cost of a bucket of hash-named objects
// no final file ever appeared in. A whole-file PUT answered the same
// durability — the staging file and the manifest row are the resume points —
// so the chunk layer is gone rather than kept as a second representation.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/riipandi/tango/internal/config"
)

// ErrNotFound is what a read of an unknown key answers with.
var ErrNotFound = errors.New("storage: not found")

// ErrInvalidKey is what a key that names nothing inside the storage tree
// answers with: an empty key, an absolute path, a ".." segment, or a name no
// sane file system stores.
var ErrInvalidKey = errors.New("storage: invalid key")

// Store is the final-file surface a backend answers. The bytes of a key
// arrive whole or not at all — an object store PUT is atomic, and the local
// driver writes through a temp file and a rename — so a caller never
// observes half of one.
//
// Every method carries the caller's context: both backends can be a network
// away, and a cancelled request must not pay for one.
type Store interface {
	// Get streams the file a key names. ErrNotFound for a key nothing
	// stored.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Put stores the file under the key, replacing what was there. size is
	// the byte count the reader holds; the local driver's rename needs it
	// and an S3 PUT carries it as the content length. contentType is what
	// a direct read of the object serves — the feature's record of what
	// the bytes are, never guessed from the key.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Delete removes the file, if present. A key already gone is the state
	// the caller asked for, not an error.
	Delete(ctx context.Context, key string) error
	// List calls fn for every key the backend holds. It is the scan the
	// garbage collection walks; fn may return an error to stop the scan.
	List(ctx context.Context, fn func(key string) error) error
}

// New hands back the driver the configuration names. The composition root
// calls it; a driver the configuration selects is the only one constructed,
// so a local deployment never opens an object-store client.
func New(cfg config.Config) (Store, error) {
	switch cfg.Storage.Driver {
	case config.StorageS3:
		return NewS3(cfg.Storage.S3)
	default:
		return NewFS(cfg.Storage.LocalPath), nil
	}
}

// ctxReader stops a copy when the context ends. Neither `sha256` nor the
// local file system watches a context, so the sync's hash pass and `FS.Put`
// wrap their source in this reader; without it a copy outlasting the queue's
// release window keeps the row claimed while the first worker is still
// inside `io.Copy`. The check is per read, so a read already underway
// completes and the stop is bounded by one read.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// Key builds a storage key from flexible parts, the naming a multi-purpose
// store needs: the first part is the purpose the files belong to — avatar,
// user-files, export — the rest free, so a feature names its files its own
// way without the engine knowing the scheme:
//
//	Key("avatar", userID, "128.png")     → avatar/usr_123/128.png
//	Key("user-files", userID, report)    → user-files/usr_123/q4-report.pdf
//
// Each part becomes one path segment: characters outside letters, digits,
// dot, dash, and underscore become underscores, and a part that is empty or
// "." or ".." — the shapes a traversal or a hidden file is made of — is
// refused with ErrInvalidKey. The caller decides the segments; the engine
// only guarantees every key names a path inside its directories.
func Key(parts ...string) (string, error) {
	if len(parts) == 0 {
		return "", ErrInvalidKey
	}
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || !hasSafeRune(part) {
			return "", fmt.Errorf("%w: empty or reserved part %q", ErrInvalidKey, part)
		}
		segment := sanitizeSegment(part)
		if segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return "", fmt.Errorf("%w: reserved part %q", ErrInvalidKey, part)
		}
		segments = append(segments, segment)
	}
	return strings.Join(segments, "/"), nil
}

// ValidateKey checks a key the caller composed itself, the same rules Key
// applies per part: no traversal, no hidden or empty segment, no separator
// tricks.
func ValidateKey(key string) error {
	if key == "" || strings.ContainsRune(key, '\\') {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	if strings.HasPrefix(key, "/") {
		return fmt.Errorf("%w: %q is absolute", ErrInvalidKey, key)
	}
	for segment := range strings.SplitSeq(key, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return fmt.Errorf("%w: %q has segment %q", ErrInvalidKey, key, segment)
		}
		if segment != sanitizeSegment(segment) {
			return fmt.Errorf("%w: %q has unsafe segment %q", ErrInvalidKey, key, segment)
		}
	}
	return nil
}

// sanitizeSegment maps one part onto a safe path segment: letters, digits,
// dot, dash, and underscore stay, everything else — spaces, slashes, unicode
// punctuation — becomes an underscore. A part built of nothing else is the
// caller's error to see, not a silent "unnamed" file.
func sanitizeSegment(part string) string {
	var b strings.Builder
	b.Grow(len(part))
	for _, r := range part {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// hasSafeRune reports whether a part survives sanitizing with anything left
// of itself: a part of only replaced characters names no file.
func hasSafeRune(part string) bool {
	for _, r := range part {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			return true
		}
	}
	return false
}
