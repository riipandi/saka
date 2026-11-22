package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	engine "github.com/riipandi/saka/framework/storage"
)

func TestEveryServedObjectIsSandboxedAndOnlyRasterImagesRenderInline(t *testing.T) {
	cases := map[string]bool{
		"image/png":                true,
		"image/jpeg":               true,
		"image/webp":               true,
		"image/avif":               true,
		"image/gif":                true,
		"image/png; charset=utf-8": true,
		"image/svg+xml":            false,
		"text/html":                false,
		"text/html; charset=utf-8": false,
		"application/javascript":   false,
		"application/pdf":          false,
		"application/octet-stream": false,
		"not a media type":         false,
	}
	for contentType, inline := range cases {
		t.Run(contentType, func(t *testing.T) {
			source := &fakeSource{manifest: engine.Manifest{Metadata: map[string]any{"content_type": contentType}}}
			rec := httptest.NewRecorder()
			engineMount(t, source, testSigner(t)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/storage/devbucket/k", nil))

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, servedPolicy, rec.Header().Get("Content-Security-Policy"))
			assert.Contains(t, servedPolicy, "sandbox")
			assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
			if inline {
				assert.Empty(t, rec.Header().Get("Content-Disposition"))
			} else {
				assert.Equal(t, "attachment", rec.Header().Get("Content-Disposition"))
			}
		})
	}
}
