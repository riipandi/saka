//go:build !release

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/riipandi/saka/framework/bundler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTheNonDocumentRequestsRideTheCompiler(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/src/main.tsx", r.URL.Path, "the proxy forwards the path the page asked for")
		_, _ = w.Write([]byte("module-body"))
	}))
	defer upstream.Close()

	r := chi.NewRouter()
	bundler.MountDev(r, upstream.URL, DefaultPage, surfacePrefixes...)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/src/main.tsx", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "module-body",
		"a module request is the compiler's to answer, not a document")
}

func TestSetupStaticJSONFallbacks(t *testing.T) {
	r := chi.NewRouter()
	SetupStatic(r)

	for _, path := range []string{"/api/missing", "/.well-known/missing", "/storage/missing.js"} {
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
	req := httptest.NewRequest(http.MethodGet, "/some-page", nil)
	req.Header.Set("Accept", "text/html")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "<title>Monolith Go React</title>")
	assert.Contains(t, body, `name="robots" content="noindex`, "the application page stays unlisted until a page says otherwise")
	assert.Contains(t, body, `src="/@vite/client"`, "the debug shell's tags are same-origin")
	assert.Contains(t, body, `src="/src/main.tsx"`, "the shell loads the page's entry, not a hard-coded main")
	assert.Contains(t, body, `src="/virtual:stylex.js"`,
		"the StyleX dev runtime rides the shell — transformIndexHtml cannot inject it into a Go-owned document")
	assert.NotContains(t, body, "http://localhost:3", "the document names no other port")
	assert.Contains(t, body, "s-loader", "the loading state rides the shell")
}
