package static_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/transport/static"
)

// serve runs one request through the local driver over dir.
func serve(t *testing.T, dir, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	static.Handler(dir).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestAServedFileComesBack(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "avatar.png"), []byte("bytes"), 0o600))

	res := serve(t, dir, "/static/avatar.png")
	assert.Equal(t, http.StatusOK, res.Code)
	assert.Equal(t, "bytes", res.Body.String())
}

func TestANestedFileComesBack(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "users", "7"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "users", "7", "photo.jpg"), []byte("photo"), 0o600))

	res := serve(t, dir, "/static/users/7/photo.jpg")
	assert.Equal(t, http.StatusOK, res.Code)
	assert.Equal(t, "photo", res.Body.String())
}

// TestAMissingUploadIsNotFound is the boundary against the SPA: the SPA
// answers an unknown path with index.html, which for an image is a broken
// <img> rather than a visible error. A missing upload must be a plain 404.
func TestAMissingUploadIsNotFound(t *testing.T) {
	res := serve(t, t.TempDir(), "/static/nope.png")
	assert.Equal(t, http.StatusNotFound, res.Code)
	assert.NotContains(t, res.Body.String(), "<html")
}

// TestADirectoryIsNotListed is the enumeration boundary: a key that names a
// directory must not answer with the names of the files under it. The client
// that holds a file's URL has it; one that does not must learn nothing.
func TestADirectoryIsNotListed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "users", "7"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "users", "7", "private.png"), []byte("x"), 0o600))

	for _, path := range []string{"/static/users/", "/static/users/7/", "/static/users/7"} {
		t.Run(path, func(t *testing.T) {
			res := serve(t, dir, path)
			assert.Equal(t, http.StatusNotFound, res.Code)
			assert.NotContains(t, res.Body.String(), "private.png")
		})
	}
}

// TestAnEmptyDataDirectoryIsNotAnError covers a run that has never stored an
// upload: the route exists and answers 404, rather than failing the run.
func TestAnEmptyDataDirectoryIsNotAnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-created")
	res := serve(t, missing, "/static/x.png")

	assert.Equal(t, http.StatusNotFound, res.Code)
}

// TestARequestCannotLeaveTheUploadsDirectory is the security rule: a request
// path that escapes the served directory must be refused, not resolved.
func TestARequestCannotLeaveTheUploadsDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "uploads")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	// A file beside the uploads directory: readable only by escaping it.
	require.NoError(t, os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("secret"), 0o600))

	for _, path := range []string{
		"/static/../secret.txt",
		"/static/a/../../secret.txt",
		`/static/..\secret.txt`,
	} {
		t.Run(path, func(t *testing.T) {
			res := serve(t, dir, path)
			assert.NotEqual(t, http.StatusOK, res.Code, "%s must not be served", path)
			assert.NotEqual(t, "secret", res.Body.String())
		})
	}
}

// TestAServedFileIsCacheableAndNotSniffed pins the two headers: the content is
// named by its path, so a client may keep it, and a browser must not guess a
// type from the bytes.
func TestAServedFileIsCacheableAndNotSniffed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.bin"), []byte("x"), 0o600))

	res := serve(t, dir, "/static/f.bin")
	assert.Contains(t, res.Header().Get("Cache-Control"), "public")
	assert.Contains(t, res.Header().Get("Cache-Control"), "immutable")
	assert.Equal(t, "nosniff", res.Header().Get("X-Content-Type-Options"))
}

// TestAMissingUploadIsNotCached is the other half of the caching rule: the
// header above describes the bytes of a file that exists, so a client must not
// keep the miss and never see the file a later write stores.
func TestAMissingUploadIsNotCached(t *testing.T) {
	res := serve(t, t.TempDir(), "/static/nope.png")
	assert.Empty(t, res.Header().Get("Cache-Control"))
}

// fakeUpload records the key it was handed and answers with the outcome the
// test set, which is what proves the mount asks a driver rather than reading
// the filesystem itself.
type fakeUpload struct {
	key  string
	err  error
	body string
}

func (f *fakeUpload) Serve(_ context.Context, w http.ResponseWriter, _ *http.Request, key string) error {
	f.key = key
	if f.err != nil {
		return f.err
	}
	_, _ = w.Write([]byte(f.body))
	return nil
}

// mounted returns a router with the uploads mount on it, for a test that drives
// the mount itself rather than the whole pipeline.
func mounted(driver static.Upload) http.Handler {
	r := chi.NewRouter()
	static.Mount(r, driver)
	return r
}

// TestTheMountAsksTheDriver proves the route is not wired to the local driver:
// a driver over something else answers through the same mount, and it is handed
// the storage key the request named.
func TestTheMountAsksTheDriver(t *testing.T) {
	driver := &fakeUpload{body: "from the driver"}

	rec := httptest.NewRecorder()
	mounted(driver).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/avatar/7.png", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "from the driver", rec.Body.String())
	assert.Equal(t, "avatar/7.png", driver.key)
}

// TestTheMountTurnsAMissIntoANotFound keeps the not-found boundary in the one
// place, so a driver only has to say a key is missing, not how to say it.
func TestTheMountTurnsAMissIntoANotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	mounted(&fakeUpload{err: static.ErrNotFound}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/nope.png", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestTheMountReportsADriverFailureAsAServerError keeps a broken driver from
// being read as a missing file: the two are different answers to a client.
func TestTheMountReportsADriverFailureAsAServerError(t *testing.T) {
	rec := httptest.NewRecorder()
	mounted(&fakeUpload{err: errors.New("backend unreachable")}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/x.png", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestDirIsUnderTheDataDirectory keeps the served tree and the file
// engine's tree apart: an upload is written whole under uploads/, never
// among the engine's files.
func TestDirIsUnderTheDataDirectory(t *testing.T) {
	assert.Equal(t, filepath.Join("storage", "uploads"), static.Dir("storage"))
	assert.Equal(t, filepath.Join(".", "uploads"), static.Dir(""))
}
