//go:build !release

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupStaticJSONFallbacks(t *testing.T) {
	r := chi.NewRouter()
	SetupStatic(r)

	for _, path := range []string{"/api/missing", "/.well-known/missing", "/static/missing.js"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

		assert.Equal(t, http.StatusNotFound, w.Code, path)
		assert.Contains(t, w.Body.String(), "not found", path)
	}
}

func TestSetupStaticRendersTheShell(t *testing.T) {
	r := chi.NewRouter()
	SetupStatic(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/some-page", nil))

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "<title>Monolith Go React</title>")
	assert.Contains(t, body, `name="robots" content="noindex`, "the application page stays unlisted until a page says otherwise")
	assert.Contains(t, body, `src="`+viteDevURL+`/@vite/client"`, "the debug shell points at the dev server")
	assert.Contains(t, body, `src="`+viteDevURL+`/app/main.tsx"`, "the shell loads the page's entry, not a hard-coded main")
	assert.Contains(t, body, "s-loader", "the loading state rides the shell")
}
