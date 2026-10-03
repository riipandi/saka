package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/storage"
)

// The failures the service defines. The handler maps them onto the codes
// the Connect protocol carries; the service defines what happened, not how
// it is answered.
var (
	// ErrBucketNotFound is a name that names no bucket.
	ErrBucketNotFound = errors.New("storage: bucket not found")

	// ErrBucketExists is a name the buckets table already holds.
	ErrBucketExists = errors.New("storage: bucket name already in use")

	// ErrBucketNotEmpty is a deletion of a bucket whose objects table still
	// holds rows — finals and staging intents alike.
	ErrBucketNotEmpty = errors.New("storage: bucket is not empty")

	// ErrBucketIsDefault is a deletion of the bucket the default-bucket
	// setting names: half the writers would lose their target on the next
	// stage.
	ErrBucketIsDefault = errors.New("storage: bucket is the default bucket")

	// ErrInvalidBucketName is a name that fails the one-segment slug rule or
	// names a data directory the engine reserves for itself.
	ErrInvalidBucketName = errors.New("storage: invalid bucket name")
)

// settingsReader is the default-bucket setting's runtime source, read fresh
// at every deletion so an operator's change takes effect at once.
// *appconfig.Settings satisfies it; the interface keeps the appconfig
// feature out of this one's import graph.
type settingsReader interface {
	GetString(ctx context.Context, key string) (string, error)
}

// SettingStorageDefaultBucket is the catalog key the deletion guard reads.
// The catalog owns the name; this constant is how this package spells it.
const SettingStorageDefaultBucket = "storage.default_bucket"

// Service carries the rules of the buckets: what a valid name is, when a
// deletion is allowed, and what a change records. The repository carries
// the SQL.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// audit writes the record of every bucket change, in the transaction
	// that changes the bucket. A removal and the record of it commit
	// together, so the log cannot describe a bucket that still exists.
	audit *audit.Recorder
	log   *slog.Logger
	// settings reads the default-bucket setting at call time. Nil keeps the
	// engine's built-in default — the state a bare wiring is in.
	settings settingsReader
}

// NewService builds the service over the shared pool.
func NewService(pool *datastore.Postgres, recorder *audit.Recorder, log *slog.Logger) *Service {
	return &Service{
		pool:  pool,
		repo:  NewRepository(),
		audit: recorder,
		log:   log,
	}
}

// WithSettings wires the default-bucket setting's runtime source. Nil keeps
// the engine's built-in default.
func (s *Service) WithSettings(settings settingsReader) *Service {
	s.settings = settings
	return s
}

// defaultBucketName answers the bucket name a deletion must refuse. The
// setting read is best-effort: an unreadable catalog falls back to the
// engine's built-in default rather than unguarding the seeded bucket.
func (s *Service) defaultBucketName(ctx context.Context) string {
	if s.settings == nil {
		return storage.DefaultBucketName
	}
	value, err := s.settings.GetString(ctx, SettingStorageDefaultBucket)
	if err != nil || value == "" {
		return storage.DefaultBucketName
	}
	return value
}

// CreateParams carries the fields a bucket is made of.
type CreateParams struct {
	Name             string
	FileSizeLimit    *int64
	AllowedMimeTypes []string
}

// UpdateParams carries the limit changes. A nil field keeps its stored
// value.
type UpdateParams struct {
	FileSizeLimit    *int64
	AllowedMimeTypes *[]string
}

// Create registers a new bucket. The name rule is the engine's own — one
// path segment, lowercase, none of the data-directory names — because a
// bucket that fails it would either break the `/storage` URL or hide a
// subtree the engine owns on disk.
func (s *Service) Create(ctx context.Context, actor string, params CreateParams) (BucketSchema, error) {
	if err := storage.ValidateBucketName(params.Name); err != nil {
		return BucketSchema{}, ErrInvalidBucketName
	}

	var created BucketSchema
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		id, insertErr := s.repo.Insert(ctx, tx, params.Name, params.FileSizeLimit, params.AllowedMimeTypes)
		if errUniqueViolation(insertErr) {
			return ErrBucketExists
		}
		if insertErr != nil {
			return fmt.Errorf("storage: create bucket: %w", insertErr)
		}

		row, readErr := s.repo.GetByName(ctx, tx, params.Name)
		if readErr != nil {
			return readErr
		}
		created = row

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventStorageBucketCreated,
			Status:       audit.StatusSuccess,
			UserID:       actor,
			ResourceType: ResourceBucket,
			ResourceID:   id.String(),
			Payload:      map[string]string{"name": params.Name},
		})
		return nil
	})
	if err != nil {
		return BucketSchema{}, err
	}
	return created, nil
}

// Get answers one bucket by name.
func (s *Service) Get(ctx context.Context, name string) (BucketSchema, error) {
	row, err := s.repo.GetByName(ctx, s.pool, name)
	if errors.Is(err, datastore.ErrNoRows) {
		return BucketSchema{}, ErrBucketNotFound
	}
	if err != nil {
		return BucketSchema{}, err
	}
	return row, nil
}

// List answers every bucket, oldest first.
func (s *Service) List(ctx context.Context) ([]BucketSchema, error) {
	return s.repo.List(ctx, s.pool)
}

// Update rewrites a bucket's limits. The name itself is immutable — it is
// the first segment of every stored reference and every URL already
// published — so only the limits move. A request that names no limit is a
// pure existence check: the row is read and answered, nothing is written,
// and no event is recorded, because no change happened.
func (s *Service) Update(ctx context.Context, actor, name string, params UpdateParams) (BucketSchema, error) {
	if params.FileSizeLimit == nil && params.AllowedMimeTypes == nil {
		return s.Get(ctx, name)
	}

	var updated BucketSchema
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		matched, updateErr := s.repo.Update(ctx, tx, name, params.FileSizeLimit, params.AllowedMimeTypes)
		if updateErr != nil {
			return updateErr
		}
		if !matched {
			return ErrBucketNotFound
		}

		row, readErr := s.repo.GetByName(ctx, tx, name)
		if readErr != nil {
			return readErr
		}
		updated = row

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventStorageBucketUpdated,
			Status:       audit.StatusSuccess,
			UserID:       actor,
			ResourceType: ResourceBucket,
			ResourceID:   row.ID.String(),
			Payload:      map[string]string{"name": name},
		})
		return nil
	})
	if err != nil {
		return BucketSchema{}, err
	}
	return updated, nil
}

// Delete removes an empty bucket. Two refusals guard it: a bucket whose
// objects table still holds rows — finals and staging intents alike — and
// the bucket the default-bucket setting names, because the stage path would
// resolve to nothing on the next write. The order of the checks is the
// order an administrator can act on: emptiness first, since emptying the
// bucket is the caller's job, then the default, which a setting change
// lifts.
func (s *Service) Delete(ctx context.Context, actor, name string) error {
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, getErr := s.repo.GetByName(ctx, tx, name)
		if errors.Is(getErr, datastore.ErrNoRows) {
			return ErrBucketNotFound
		}
		if getErr != nil {
			return getErr
		}

		count, countErr := s.repo.CountObjects(ctx, tx, row.ID)
		if countErr != nil {
			return countErr
		}
		if count > 0 {
			return ErrBucketNotEmpty
		}
		if name == s.defaultBucketName(ctx) {
			return ErrBucketIsDefault
		}

		if _, delErr := s.repo.Delete(ctx, tx, name); delErr != nil {
			return delErr
		}

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventStorageBucketDeleted,
			Status:       audit.StatusSuccess,
			UserID:       actor,
			ResourceType: ResourceBucket,
			ResourceID:   row.ID.String(),
			Payload:      map[string]string{"name": name},
		})
		return nil
	})
}

// errUniqueViolation reads the unique-index conflict a duplicate name's
// insert answers.
func errUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
