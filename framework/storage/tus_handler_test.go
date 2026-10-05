package storage

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingEnqueuer answers the engine's completion seam with a slice a
// test reads.
type recordingEnqueuer struct {
	refs []string
}

func (e *recordingEnqueuer) EnqueueUpload(_ context.Context, ref string) error {
	e.refs = append(e.refs, ref)
	return nil
}

// newTusHarness builds the container-backed manager the manager tests share
// and the handler over it; the enqueuer's slice is what the completion
// assertions read.
func newTusHarness(t *testing.T) (*httptest.Server, *Manager, *recordingEnqueuer) {
	t.Helper()

	manager, _, _ := newManager(t)
	enqueuer := &recordingEnqueuer{}
	manager.WithUploadEnqueuer(enqueuer)
	server := httptest.NewServer(NewTusHandler(manager, manager.log))
	t.Cleanup(server.Close)
	return server, manager, enqueuer
}

// metadataHeader encodes the pairs the tus creation carries: each is a
// space-separated `name value` whose value rides base64.
func metadataHeader(pairs ...[2]string) string {
	encoded := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		encoded = append(encoded, pair[0]+" "+base64.StdEncoding.EncodeToString([]byte(pair[1])))
	}
	return strings.Join(encoded, ",")
}

func tusCreation(t *testing.T, server *httptest.Server, bucket, key string, length int64, body *string) *http.Response {
	t.Helper()

	var reader *strings.Reader
	if body != nil {
		reader = strings.NewReader(*body)
	} else {
		reader = strings.NewReader("")
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/uploads", reader)
	require.NoError(t, err)
	request.Header.Set("Upload-Length", formatInt(length))
	request.Header.Set("Upload-Metadata", metadataHeader(
		[2]string{"bucket", bucket},
		[2]string{"key", key},
		[2]string{"filetype", "text/plain"},
	))
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func tusPatch(t *testing.T, server *httptest.Server, bucket, key string, offset int64, body string) *http.Response {
	t.Helper()

	request, err := http.NewRequest(http.MethodPatch, server.URL+"/api/uploads/"+bucket+"/"+key, strings.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Content-Type", TusMedia)
	request.Header.Set("Upload-Offset", formatInt(offset))
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func tusHead(t *testing.T, server *httptest.Server, bucket, key string) *http.Response {
	t.Helper()

	request, err := http.NewRequest(http.MethodHead, server.URL+"/api/uploads/"+bucket+"/"+key, nil)
	require.NoError(t, err)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func formatInt(n int64) string {
	return strconv.FormatInt(n, 10)
}

func TestTusCreationStagesAnEmptySession(t *testing.T) {
	server, manager, _ := newTusHarness(t)

	response := tusCreation(t, server, testBucket, "probe/creation.txt", 100, nil)
	assert.Equal(t, http.StatusCreated, response.StatusCode)
	assert.Equal(t, TusVersion, response.Header.Get("Tus-Resumable"))
	assert.Equal(t, "/api/uploads/"+testBucket+"/probe/creation.txt", response.Header.Get("Location"))
	assert.Equal(t, "0", response.Header.Get("Upload-Offset"))

	size, err := manager.stagingSize(testBucket, "probe/creation.txt")
	require.NoError(t, err)
	assert.Zero(t, size, "creation stages an empty file")
}

func TestTusCreationCarriesTheFirstChunk(t *testing.T) {
	server, manager, _ := newTusHarness(t)

	body := "01234"
	response := tusCreation(t, server, testBucket, "probe/first.txt", 10, &body)
	assert.Equal(t, http.StatusCreated, response.StatusCode)
	assert.Equal(t, "5", response.Header.Get("Upload-Offset"))

	size, err := manager.stagingSize(testBucket, "probe/first.txt")
	require.NoError(t, err)
	assert.EqualValues(t, 5, size)
}

func TestTusCreationRefusesAMalformedRequest(t *testing.T) {
	server, _, _ := newTusHarness(t)

	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/uploads", strings.NewReader(""))
	require.NoError(t, err)
	request.Header.Set("Upload-Length", "10")
	// The pair's value is not valid base64, so the creation is not one the
	// protocol defined.
	request.Header.Set("Upload-Metadata", "bucket !!!!,key aGVsbG8")
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)

	request, err = http.NewRequest(http.MethodPost, server.URL+"/api/uploads", strings.NewReader(""))
	require.NoError(t, err)
	request.Header.Set("Upload-Metadata", metadataHeader([2]string{"bucket", testBucket}, [2]string{"key", "probe.txt"}))
	response, err = server.Client().Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusBadRequest, response.StatusCode, "a creation without a length is refused")
}

func TestTusCreationRefusesAnUnknownBucket(t *testing.T) {
	server, _, _ := newTusHarness(t)

	response := tusCreation(t, server, "missing", "probe.txt", 10, nil)
	assert.Equal(t, http.StatusNotFound, response.StatusCode)
}

func TestTusCreationRefusesAnOversizedUpload(t *testing.T) {
	pool := migratedPool(t)
	seedBucket(t, pool, testBucket)
	store := NewFS(t.TempDir())
	manager := NewManager(store, pool, t.TempDir(), slog.New(slog.DiscardHandler))
	enqueuer := &recordingEnqueuer{}
	manager.WithUploadEnqueuer(enqueuer)
	server := httptest.NewServer(NewTusHandler(manager, manager.log))
	t.Cleanup(server.Close)

	// The bucket's size limit is the ceiling the creation's declared
	// length is held to, before any byte travels.
	_, err := pool.Exec(t.Context(),
		`UPDATE storage_buckets SET file_size_limit = $1 WHERE name = $2`, 5, testBucket)
	require.NoError(t, err)

	response := tusCreation(t, server, testBucket, "probe/limit.txt", 10, nil)
	assert.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
}

func TestTusAppendRejectsAMisplacedOffset(t *testing.T) {
	server, _, _ := newTusHarness(t)

	created := tusCreation(t, server, testBucket, "probe/offset.txt", 100, nil)
	require.Equal(t, http.StatusCreated, created.StatusCode)

	response := tusPatch(t, server, testBucket, "probe/offset.txt", 7, "0123456789")
	assert.Equal(t, http.StatusConflict, response.StatusCode, "a chunk at an offset the file has not reached is refused")
}

func TestTusResumeContinuesFromTheServerOffset(t *testing.T) {
	server, manager, _ := newTusHarness(t)

	body := "01234"
	created := tusCreation(t, server, testBucket, "probe/resume.txt", 10, &body)
	require.Equal(t, http.StatusCreated, created.StatusCode)

	// A resuming client asks first; the answer names the five bytes the
	// creation already carried.
	headed := tusHead(t, server, testBucket, "probe/resume.txt")
	require.Equal(t, http.StatusOK, headed.StatusCode)
	assert.Equal(t, "5", headed.Header.Get("Upload-Offset"))
	assert.Equal(t, "no-store", headed.Header.Get("Cache-Control"))

	// The next chunk claims the offset the HEAD answered, and the answer
	// carries the file's new end.
	appended := tusPatch(t, server, testBucket, "probe/resume.txt", 5, "56789")
	require.Equal(t, http.StatusNoContent, appended.StatusCode)
	assert.Equal(t, "10", appended.Header.Get("Upload-Offset"))

	size, err := manager.stagingSize(testBucket, "probe/resume.txt")
	require.NoError(t, err)
	assert.EqualValues(t, 10, size)
}

func TestTusAppendRejectsAWrongMediaType(t *testing.T) {
	server, _, _ := newTusHarness(t)

	created := tusCreation(t, server, testBucket, "probe/media.txt", 100, nil)
	require.Equal(t, http.StatusCreated, created.StatusCode)

	request, err := http.NewRequest(http.MethodPatch, server.URL+"/api/uploads/"+testBucket+"/probe/media.txt", strings.NewReader("chunk"))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("Upload-Offset", "0")
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusUnsupportedMediaType, response.StatusCode)
}

func TestTusTerminationRemovesTheSession(t *testing.T) {
	server, manager, _ := newTusHarness(t)

	created := tusCreation(t, server, testBucket, "probe/terminate.txt", 100, nil)
	require.Equal(t, http.StatusCreated, created.StatusCode)

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/uploads/"+testBucket+"/probe/terminate.txt", nil)
	require.NoError(t, err)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusNoContent, response.StatusCode)

	_, statErr := os.Stat(manager.stagingPath(testBucket, "probe/terminate.txt"))
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "the staging file is gone")
}

func TestTusExpirationReclaimsAnInterruptedSession(t *testing.T) {
	server, manager, _ := newTusHarness(t)

	created := tusCreation(t, server, testBucket, "probe/expired.txt", 100, nil)
	require.Equal(t, http.StatusCreated, created.StatusCode)

	// The window a session lives is the engine's constant; the expiry read
	// one window past the session's start reclaims it.
	expired, err := manager.ExpireSessions(t.Context(), time.Now().Add(TusSessionExpiry+time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, expired)

	headed := tusHead(t, server, testBucket, "probe/expired.txt")
	assert.Equal(t, http.StatusNotFound, headed.StatusCode, "the row and the file went together")
}

func TestTusCompletionEnqueuesTheUpload(t *testing.T) {
	server, _, enqueuer := newTusHarness(t)

	body := "01234"
	created := tusCreation(t, server, testBucket, "probe/complete.txt", 10, &body)
	require.Equal(t, http.StatusCreated, created.StatusCode)

	// The final chunk reached the declared length, so the enqueue is the
	// completion's side effect, not the client's second request.
	appended := tusPatch(t, server, testBucket, "probe/complete.txt", 5, "56789")
	require.Equal(t, http.StatusNoContent, appended.StatusCode)
	assert.Equal(t, []string{testBucket + "/probe/complete.txt"}, enqueuer.refs)

	// A chunk past the declared length is a mismatch, not a silence.
	past := tusPatch(t, server, testBucket, "probe/complete.txt", 10, "x")
	assert.Equal(t, http.StatusConflict, past.StatusCode)
}

// TestTheStartupSweepRequeuesStagePathFilesOnly pins the sweep's predicate:
// a Stage-path file the run never enqueued is requeued at boot, while a tus
// session's interrupted bytes are the expiry job's, not the sweep's. The
// sweep itself is the jobs package's — the engine only answers the refs.
func TestTheStartupSweepRequeuesStagePathFilesOnly(t *testing.T) {
	server, manager, enqueuer := newTusHarness(t)
	ctx := t.Context()

	// A Stage-path file: pending, no session marker — the sweep's rows.
	require.NoError(t, manager.Stage(ctx, testBucket, "docs/staged.txt", strings.NewReader("staged"), nil))

	// An interrupted tus session: pending with a marker — the expiry's row.
	body := "01234"
	created := tusCreation(t, server, testBucket, "probe/session.txt", 10, &body)
	require.Equal(t, http.StatusCreated, created.StatusCode)

	refs, err := manager.PendingStagedRefs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{testBucket + "/docs/staged.txt"}, refs)
	assert.Empty(t, enqueuer.refs, "nothing enqueued before the sweep runs")
}

func TestTusOptionsNamesTheExtensions(t *testing.T) {
	server, _, _ := newTusHarness(t)

	request, err := http.NewRequest(http.MethodOptions, server.URL+"/api/uploads", nil)
	require.NoError(t, err)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	assert.Equal(t, TusVersion, response.Header.Get("Tus-Version"))
	assert.Contains(t, response.Header.Get("Tus-Extension"), "creation-with-upload")
	assert.Contains(t, response.Header.Get("Tus-Extension"), "termination")
	assert.Contains(t, response.Header.Get("Tus-Extension"), "expiration")
}
