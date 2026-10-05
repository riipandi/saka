package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	engine "github.com/riipandi/saka/framework/storage"
)

// discardLogger is the logger the tests' managers run under.
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// engineMount builds the /storage handler over a fake source, the shape
// the router's own mount call composes.
func engineMount(t *testing.T, source ManagerSource, signer *engine.Signer) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	MountStorage(r, source, signer)
	return r
}

// testSecretHex is 32 bytes of hex — the parse floor an HMAC key needs.
const testSecretHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeSource is the manifest/open pair the handler tests run over. The
// manifest it hands back is the test's lever for the visibility flag.
type fakeSource struct {
	manifest engine.Manifest
}

func (f *fakeSource) Manifest(context.Context, string, string) (engine.Manifest, error) {
	return f.manifest, nil
}

func (f *fakeSource) Open(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("private-bytes")), nil
}

func testSigner(t *testing.T) *engine.Signer {
	t.Helper()
	signer, err := engine.NewSigner(testSecretHex, "")
	require.NoError(t, err)
	return signer
}

func TestAPublicObjectServesWithTheImmutableCacheHeader(t *testing.T) {
	mount := engineMount(t, &fakeSource{manifest: engine.Manifest{Metadata: map[string]any{}}}, testSigner(t))

	rec := httptest.NewRecorder()
	mount.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/storage/devbucket/k.txt", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable")
}

func TestAPrivateObjectRefusesAPlainReadWithTheMissingShape(t *testing.T) {
	mount := engineMount(t, &fakeSource{manifest: engine.Manifest{IsPrivate: true}}, testSigner(t))

	rec := httptest.NewRecorder()
	mount.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/storage/devbucket/k.txt", nil))

	// The refusal is the missing object's 404, never a 403 that confirms
	// the key exists, and it carries no cache header a client could keep.
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, rec.Header().Get("Cache-Control"))
}

func TestAPrivateObjectServesBehindAValidLink(t *testing.T) {
	signer := testSigner(t)
	source := &fakeSource{manifest: engine.Manifest{IsPrivate: true}}
	mount := engineMount(t, source, signer)

	url, err := signer.SignedURL("/storage", "devbucket", "k.txt", time.Now().Add(time.Hour))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	mount.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "private-bytes", rec.Body.String())
}

func TestAPrivateObjectRefusesAnExpiredOrTamperedLink(t *testing.T) {
	signer := testSigner(t)
	source := &fakeSource{manifest: engine.Manifest{IsPrivate: true}}
	mount := engineMount(t, source, signer)

	expired, err := signer.SignedURL("/storage", "devbucket", "k.txt", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	tampered := strings.Replace(expired, "devbucket", "staging", 1)

	for name, url := range map[string]string{"expired": expired, "tampered": tampered} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mount.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestAPrivateObjectWithoutASignerFailsClosed(t *testing.T) {
	mount := engineMount(t, &fakeSource{manifest: engine.Manifest{IsPrivate: true}}, nil)

	rec := httptest.NewRecorder()
	mount.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/storage/devbucket/k.txt?exp=9999999999&sig=aa", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestManagerSignedURLResolvesTheTTLFromTheWiredSource(t *testing.T) {
	signer := testSigner(t)
	manager := engine.NewManager(nil, nil, t.TempDir(), discardLogger()).
		WithSigner(signer).
		WithSignedURLTTL(func(context.Context) (time.Duration, error) {
			return 90 * time.Second, nil
		})

	url, err := manager.SignedURL(context.Background(), "http://x.test/storage", "devbucket", "k.txt", 0)
	require.NoError(t, err)
	assert.Contains(t, url, "exp=")
	assert.Contains(t, url, "sig=")
}

func TestManagerSignedURLRefusesWithoutASignerOrASource(t *testing.T) {
	bare := engine.NewManager(nil, nil, t.TempDir(), discardLogger())
	_, err := bare.SignedURL(context.Background(), "/storage", "devbucket", "k.txt", 0)
	assert.Error(t, err)

	noSource := bare.WithSigner(testSigner(t))
	_, err = noSource.SignedURL(context.Background(), "/storage", "devbucket", "k.txt", 0)
	assert.Error(t, err)
}
