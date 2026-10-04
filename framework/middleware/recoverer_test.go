package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/webutil"
)

func TestRecovererAnswersAPanicWithAnEnvelope(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	handler := Recoverer(log)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			panic("boom")
		}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/things", nil)
	req = req.WithContext(webutil.WithRequestID(req.Context(), "req_abc123"))
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var body struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "error", body.Status)
	assert.Equal(t, "internal server error", body.Message)

	// The panic line carries what a responder needs to find the request: the
	// value, the stack, and the id the envelope metadata named.
	line := strings.TrimSpace(buf.String())
	assert.Contains(t, line, "panic recovered")
	assert.Contains(t, line, "boom")
	assert.Contains(t, line, "request_id=req_abc123")
	assert.Contains(t, line, "goroutine")
}

func TestRecovererLeavesAResponseThatAlreadyBegan(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	handler := Recoverer(log)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("half a response"))
			panic("late boom")
		}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "half a response", rec.Body.String())
	assert.Contains(t, buf.String(), "late boom")
}

func TestRecovererRepanicsTheAbortHandler(t *testing.T) {
	handler := Recoverer(slog.Default())(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			panic(http.ErrAbortHandler)
		}))

	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})
}

func TestRecovererPassesAQuietRequestThrough(t *testing.T) {
	handler := Recoverer(slog.Default())(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusNoContent, rec.Code)
}
