package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	authn "connectrpc.com/authn"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/kernel"
	"github.com/riipandi/saka/internal/storage"
	"github.com/riipandi/saka/pkg/testutils"
)

// The upload progress read: the manifest's own state, answered where the
// engine that owns it is resolved.

func progressRouter(t *testing.T, pool *datastore.Postgres, manager *storage.Manager) http.Handler {
	t.Helper()

	injector := do.New()
	do.Provide(injector, func(do.Injector) (*storage.Manager, error) { return manager, nil })
	return NewRouter(Options{
		Config:   config.Default(),
		Checker:  nil,
		Modules:  []kernel.Module{},
		Injector: injector,
		Logger:   slog.New(slog.DiscardHandler),
	})
}

func TestUploadProgressAnswersTheManifestState(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := testutils.MigratedPostgres(t, "transport_progress_test")
	store := storage.NewFS(t.TempDir())
	manager := storage.NewManager(store, pool, t.TempDir(), slog.New(slog.DiscardHandler))
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO storage_buckets (name) VALUES ('default') ON CONFLICT (name) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	require.NoError(t, manager.Stage(t.Context(), "default", "avatars/robert-langdon.png",
		bytes.NewReader(bytes.Repeat([]byte("grimoire"), 64)), nil))
	require.NoError(t, manager.Sync(t.Context(), "default/avatars/robert-langdon.png"))

	router := progressRouter(t, pool, manager)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/uploads/default/avatars/robert-langdon.png", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Status string `json:"status"`
		Data   struct {
			Key    string `json:"key"`
			Status string `json:"status"`
			Size   int64  `json:"size"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "success", body.Status)
	assert.Equal(t, "default/avatars/robert-langdon.png", body.Data.Key)
	assert.Equal(t, "ready", body.Data.Status)
	assert.Equal(t, int64(512), body.Data.Size)

	// A key nothing stored is the not-found, never an index page.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/uploads/default/avatars/nobody.png", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUploadProgressRefusesAnUnauthenticatedCaller(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := testutils.MigratedPostgres(t, "transport_progress_test")
	manager := storage.NewManager(storage.NewFS(t.TempDir()), pool, t.TempDir(), slog.New(slog.DiscardHandler))

	injector := do.New()
	do.Provide(injector, func(do.Injector) (*storage.Manager, error) { return manager, nil })
	router := NewRouter(Options{
		Config:   config.Default(),
		Checker:  nil,
		Modules:  []kernel.Module{},
		Injector: injector,
		Logger:   slog.New(slog.DiscardHandler),
		Authenticator: func(ctx context.Context, req *http.Request) (any, error) {
			return nil, authn.Errorf("authentication required")
		},
	})

	// The bearer middleware refuses before the handler runs: an
	// unauthenticated poll is the 401 the REST surface answers, and the
	// route's absence is not the answer it leaks.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/uploads/avatars/robert-langdon.png", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
