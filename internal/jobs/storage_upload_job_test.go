package jobs

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/storage"
	"github.com/riipandi/saka/pkg/testutils"
)

func TestStorageUploadJobSyncsAStagedFile(t *testing.T) {
	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)

	pool, client := migratedClient(t, dsn)
	store := storage.NewFS(t.TempDir())
	manager := storage.NewManager(store, pool, t.TempDir(), slog.New(slog.DiscardHandler))
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO storage_buckets (name) VALUES ('devbucket') ON CONFLICT (name) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	Register(client, time.Hour, manager, nil, pool, "", false, false, nil, nil, nil, nil, nil, nil, nil)

	data := bytes.Repeat([]byte("queued"), 40)
	require.NoError(t, manager.Stage(t.Context(), "devbucket", "uploads/report.bin", bytes.NewReader(data), nil))

	// The watcher's job: one task enqueued onto the durable queue, executed
	// by the dispatcher this test starts.
	if _, err := client.Add(StorageUploadTask{Key: "devbucket/uploads/report.bin"}).Save(); err != nil {
		t.Fatal(err)
	}
	client.Start(t.Context())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		client.Stop(ctx)
	})

	// The sync's effect: the manifest is ready and the staging file is gone.
	require.Eventually(t, func() bool {
		manifest, err := manager.Manifest(t.Context(), "devbucket", "uploads/report.bin")
		return err == nil && manifest.Status == storage.StatusReady
	}, 5*time.Second, 20*time.Millisecond, "the upload job must commit the manifest")

	// The staging file has served its purpose: the bytes live in the
	// backend, whole under the bucket-scoped key.
	_, statErr := os.Stat(filepath.Join(manager.Staging(), "devbucket", "uploads/report.bin"))
	assert.ErrorIs(t, statErr, os.ErrNotExist, "the staging file must be gone after the sync")
}
