package bundler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// releaseFS is a build output the test embeds in place of a real one: the
// manifest naming one entry, and the asset it resolves to. The mount is
// built from this fixture alone, so the constructor carries no asset.
func releaseFS() fstest.MapFS {
	return fstest.MapFS{
		"assets.json": &fstest.MapFile{Data: []byte(`{
			"src/main.tsx": {
				"file": "assets/app-X9d2.js", "name": "app", "src": "src/main.tsx", "isEntry": true
			}
		}`)},
		"assets/app-X9d2.js": &fstest.MapFile{Data: []byte("module-body")},
	}
}

func TestMountReleaseServesTheAssetAndTheShell(t *testing.T) {
	r := chi.NewRouter()
	MountRelease(r, releaseFS(), Page{Entry: "src/main.tsx", Title: "Test"}, "/api")

	asset := httptest.NewRecorder()
	r.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/app-X9d2.js", nil))
	require.Equal(t, http.StatusOK, asset.Code)
	assert.Equal(t, "module-body", asset.Body.String())

	doc := httptest.NewRecorder()
	r.ServeHTTP(doc, httptest.NewRequest(http.MethodGet, "/some-page", nil))
	require.Equal(t, http.StatusOK, doc.Code)
	assert.Contains(t, doc.Body.String(), `<script type="module" src="/assets/app-X9d2.js"`)
	assert.Contains(t, doc.Body.String(), "<title>Test</title>")
}

func TestMountReleaseRefusesTheReservedPrefixes(t *testing.T) {
	r := chi.NewRouter()
	MountRelease(r, releaseFS(), Page{Entry: "src/main.tsx", Title: "Test"}, "/api")

	refused := httptest.NewRecorder()
	r.ServeHTTP(refused, httptest.NewRequest(http.MethodGet, "/api/missing", nil))
	assert.Equal(t, http.StatusNotFound, refused.Code)
	assert.Contains(t, refused.Body.String(), "not found")
}

func TestMountReleaseNamesItselfWhenTheManifestIsMissing(t *testing.T) {
	r := chi.NewRouter()
	MountRelease(r, fstest.MapFS{}, Page{Entry: "src/main.tsx", Title: "Test"})

	doc := httptest.NewRecorder()
	r.ServeHTTP(doc, httptest.NewRequest(http.MethodGet, "/some-page", nil))
	require.Equal(t, http.StatusInternalServerError, doc.Code)
	assert.Contains(t, doc.Body.String(), "vite: the build manifest did not resolve")
}
