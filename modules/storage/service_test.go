package storage

import (
	"context"
	"log/slog"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	storagev1connect "github.com/riipandi/saka/codegen/proto/go/saka/storage/v1/storagev1connect"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/pkg/testutils"
)

// fakeSettings answers the default-bucket setting with a value the test
// controls, the way the catalog does in a running deployment.
type fakeSettings struct {
	value string
}

func (f *fakeSettings) GetString(ctx context.Context, key string) (string, error) {
	return f.value, nil
}

// bucketService builds the service over a migrated database, with the
// setting source the test names.
func bucketService(t *testing.T, settings settingsReader) (*Service, *datastore.Postgres) {
	t.Helper()
	testutils.SkipWithoutDocker(t)

	pool := testutils.MigratedPostgres(t, "storage_bucket_test")
	service := NewService(pool, audit.NewRecorder(slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
	service.WithSettings(settings)
	return service, pool
}

// ptrInt64 wraps a size limit for the params that carry one.
func ptrInt64(v int64) *int64 { return &v }

// TestTheWireIdCarriesTheBucketPrefix pins the identifier's wire form: a
// bucket's id leaves the server carrying the bkt TypeID's prefix, and the
// wire form parses back to the UUID the column stores.
func TestTheWireIdCarriesTheBucketPrefix(t *testing.T) {
	service, _ := bucketService(t, &fakeSettings{value: "devbucket"})
	ctx := t.Context()

	created, err := service.Create(ctx, "admin", CreateParams{Name: "gryffindor"})
	require.NoError(t, err)

	wire := FormatID(created.ID)
	require.Regexp(t, `^bkt_[a-z0-9]{26}$`, wire, "the wire id carries the bucket TypeID's prefix")

	back, err := UUIDFromWire(wire)
	require.NoError(t, err)
	assert.Equal(t, created.ID, back, "the wire form round-trips to the column's bytes")
}

// TestCreateGetAndListBuckets pins the read surface: a created bucket reads
// back by name with the limits it was created with, and the list answers it
// beside the ones already there.
func TestCreateGetAndListBuckets(t *testing.T) {
	service, _ := bucketService(t, &fakeSettings{value: "devbucket"})
	ctx := t.Context()

	created, err := service.Create(ctx, "admin", CreateParams{
		Name:          "gryffindor",
		FileSizeLimit: ptrInt64(1024),
	})
	require.NoError(t, err)
	assert.Equal(t, "gryffindor", created.Name)
	require.NotNil(t, created.FileSizeLimit)
	assert.Equal(t, int64(1024), *created.FileSizeLimit)
	assert.False(t, created.UpdatedAt.IsZero(), "the insert default stamps the row")

	read, err := service.Get(ctx, "gryffindor")
	require.NoError(t, err)
	assert.Equal(t, created.ID, read.ID)

	listed, err := service.List(ctx)
	require.NoError(t, err)
	names := map[string]bool{}
	for _, bucket := range listed {
		names[bucket.Name] = true
	}
	assert.True(t, names["gryffindor"], "the list answers the created bucket")
}

// TestCreateRefusesADuplicateName pins the unique-name rule: a second bucket
// named like the first is refused, not silently accepted.
func TestCreateRefusesADuplicateName(t *testing.T) {
	service, _ := bucketService(t, &fakeSettings{value: "devbucket"})
	ctx := t.Context()

	_, err := service.Create(ctx, "admin", CreateParams{Name: "hogwarts"})
	require.NoError(t, err)

	_, err = service.Create(ctx, "admin", CreateParams{Name: "hogwarts"})
	assert.ErrorIs(t, err, ErrBucketExists)
}

// TestCreateAcceptsAnySlugName pins the name rule's engine half: the name
// is one path segment, and the buckets live inside the engine's uploads
// container, so no slug collides with a data directory anymore — any
// lowercase slug a creation passes names a bucket.
func TestCreateAcceptsAnySlugName(t *testing.T) {
	service, _ := bucketService(t, &fakeSettings{value: "devbucket"})
	ctx := t.Context()

	for _, name := range []string{"staging", "logs", "backup", "config", "files"} {
		_, err := service.Create(ctx, "admin", CreateParams{Name: name})
		assert.NoError(t, err, "the slug %q is a bucket like any other", name)
	}
}

// TestCreateRefusesAnUppercaseName pins the slug half of the name rule: a
// bucket name is the first segment of a public URL, and the serving route
// resolves it lowercase.
func TestCreateRefusesAnUppercaseName(t *testing.T) {
	service, _ := bucketService(t, &fakeSettings{value: "devbucket"})
	ctx := t.Context()

	_, err := service.Create(ctx, "admin", CreateParams{Name: "Gryffindor"})
	assert.ErrorIs(t, err, ErrInvalidBucketName)
}

// TestGetRefusesAnUnknownName pins the not-found answer for a name the
// buckets table does not hold.
func TestGetRefusesAnUnknownName(t *testing.T) {
	service, _ := bucketService(t, &fakeSettings{value: "devbucket"})
	ctx := t.Context()

	_, err := service.Get(ctx, "nowhere")
	assert.ErrorIs(t, err, ErrBucketNotFound)
}

// TestUpdateChangesTheLimitsAndKeepsAbsentFields pins the partial update: a
// present field replaces its stored value, an absent one keeps it, and the
// name itself is never rewritten.
func TestUpdateChangesTheLimitsAndKeepsAbsentFields(t *testing.T) {
	service, _ := bucketService(t, &fakeSettings{value: "devbucket"})
	ctx := t.Context()

	_, err := service.Create(ctx, "admin", CreateParams{
		Name:             "gryffindor",
		FileSizeLimit:    ptrInt64(1024),
		AllowedMimeTypes: []string{"image/png"},
	})
	require.NoError(t, err)

	updated, err := service.Update(ctx, "admin", "gryffindor", UpdateParams{
		FileSizeLimit: ptrInt64(2048),
	})
	require.NoError(t, err)
	require.NotNil(t, updated.FileSizeLimit)
	assert.Equal(t, int64(2048), *updated.FileSizeLimit)
	assert.Equal(t, []string{"image/png"}, updated.AllowedMimeTypes, "an absent field keeps its stored value")
	require.NotNil(t, updated.UpdatedAt, "the trigger stamps the update")

	cleared, err := service.Update(ctx, "admin", "gryffindor", UpdateParams{})
	require.NoError(t, err)
	assert.Equal(t, "gryffindor", cleared.Name, "the name is immutable")
	require.NotNil(t, cleared.FileSizeLimit)
	assert.Equal(t, int64(2048), *cleared.FileSizeLimit, "an empty update keeps both limits")
}

// TestUpdateRefusesAnUnknownName pins the not-found answer for an update
// that names no bucket.
func TestUpdateRefusesAnUnknownName(t *testing.T) {
	service, _ := bucketService(t, &fakeSettings{value: "devbucket"})
	ctx := t.Context()

	_, err := service.Update(ctx, "admin", "nowhere", UpdateParams{})
	assert.ErrorIs(t, err, ErrBucketNotFound)
}

// TestDeleteRefusesANonEmptyBucket pins the emptiness rule: a bucket whose
// objects table still holds rows is refused, and a staging intent row
// counts as much as a final — the caller empties the bucket first.
func TestDeleteRefusesANonEmptyBucket(t *testing.T) {
	service, pool := bucketService(t, &fakeSettings{value: "devbucket"})
	ctx := t.Context()

	created, err := service.Create(ctx, "admin", CreateParams{Name: "gryffindor"})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO public.storage_objects (bucket_id, key, content_hash, status)
		VALUES ($1, 'pictures/hermione.png', 'hash', 'ready')`, created.ID)
	require.NoError(t, err)

	err = service.Delete(ctx, "admin", "gryffindor")
	assert.ErrorIs(t, err, ErrBucketNotEmpty)
}

// TestDeleteRefusesTheBucketTheSettingNames pins the default guard: the
// bucket the default-bucket setting names is refused even when empty,
// because the stage path would resolve to nothing on the next write.
func TestDeleteRefusesTheBucketTheSettingNames(t *testing.T) {
	service, _ := bucketService(t, &fakeSettings{value: "gryffindor"})
	ctx := t.Context()

	_, err := service.Create(ctx, "admin", CreateParams{Name: "gryffindor"})
	require.NoError(t, err)

	err = service.Delete(ctx, "admin", "gryffindor")
	assert.ErrorIs(t, err, ErrBucketIsDefault)
}

// TestDeleteFallsBackToTheEngineDefaultWithoutSettings pins the nil-safe
// read: a service with no settings source still guards the engine's built-in
// default bucket, rather than unguarding the seeded one.
func TestDeleteFallsBackToTheEngineDefaultWithoutSettings(t *testing.T) {
	service, _ := bucketService(t, nil)
	ctx := t.Context()

	// The migrated database carries no seed, so the guard answers on the
	// name alone: creating it is refused as the default the deletion would
	// leave the stage path without.
	_, err := service.Create(ctx, "admin", CreateParams{Name: "devbucket"})
	require.NoError(t, err)

	err = service.Delete(ctx, "admin", "devbucket")
	assert.ErrorIs(t, err, ErrBucketIsDefault)
}

// TestDeleteRemovesAnEmptyBucket pins the happy path: an empty bucket that
// no setting names is removed and reads back as not found.
func TestDeleteRemovesAnEmptyBucket(t *testing.T) {
	service, _ := bucketService(t, &fakeSettings{value: "devbucket"})
	ctx := t.Context()

	_, err := service.Create(ctx, "admin", CreateParams{Name: "gryffindor"})
	require.NoError(t, err)

	require.NoError(t, service.Delete(ctx, "admin", "gryffindor"))

	_, err = service.Get(ctx, "gryffindor")
	assert.ErrorIs(t, err, ErrBucketNotFound)

	err = service.Delete(ctx, "admin", "gryffindor")
	assert.ErrorIs(t, err, ErrBucketNotFound, "a second deletion names no bucket anymore")
}

// TestTheModuleClaimsEveryProcedureTheContractDeclares is the forwarding
// check: the module mounts each procedure the generated handler answers, so
// a procedure added to the contract without a mount is caught here rather
// than answering "unknown procedure" in a running server.
func TestTheModuleClaimsEveryProcedureTheContractDeclares(t *testing.T) {
	module := NewModule(nil)

	router := chi.NewRouter()
	module.MountRPC(router)

	claimed := map[string]bool{}
	for _, route := range router.Routes() {
		claimed[route.Pattern] = true
	}

	for _, procedure := range []string{
		storagev1connect.BucketServiceCreateBucketProcedure,
		storagev1connect.BucketServiceUpdateBucketProcedure,
		storagev1connect.BucketServiceDeleteBucketProcedure,
		storagev1connect.BucketServiceListBucketsProcedure,
		storagev1connect.BucketServiceGetBucketProcedure,
	} {
		assert.True(t, claimed[procedure], "the module must claim %s", procedure)
	}
	assert.Len(t, claimed, 5, "the module must claim the contract's procedures and no more")
}

// TestTheAreaForwardsTheServiceThroughTheContainer is the wiring check the
// registry relies on: the area's Package registers the service and its Mount
// resolves it, so a provider missed there leaves the procedures answering
// "unknown procedure" — a hand-built service would pass while the wiring is
// broken.
func TestTheAreaForwardsTheServiceThroughTheContainer(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := testutils.MigratedPostgres(t, "storage_bucket_wiring_test")
	logger := slog.New(slog.DiscardHandler)

	i := do.New(
		do.Eager(logger),
		do.Eager(pool),
		do.Eager(audit.NewRecorder(logger)),
	)
	Package(i)

	module, err := Mount(i)
	require.NoError(t, err)
	assert.Equal(t, ModuleName, module.Name())
}
