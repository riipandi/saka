package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestIDGeneratesATaggedID(t *testing.T) {
	var seen string

	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	id := rec.Header().Get(RequestIDHeader)
	assert.True(t, strings.HasPrefix(id, RequestIDPrefix+"_"), "generated id %q must carry the req_ prefix", id)
	assert.Equal(t, id, seen, "the context must carry the id the response names")
}

func TestRequestIDHonoursAnIncomingID(t *testing.T) {
	var seen string

	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "caller-trace-42")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, "caller-trace-42", rec.Header().Get(RequestIDHeader))
	assert.Equal(t, "caller-trace-42", seen, "a caller's id is honoured, not replaced")
}

func TestRequestIDReadsDistinctlyPerRequest(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))

	require.NotEqual(t, first.Header().Get(RequestIDHeader), second.Header().Get(RequestIDHeader),
		"two requests must not share an id")
}
