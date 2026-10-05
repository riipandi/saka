package webutil

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"encoding/json/v2"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeEnvelope decodes the response body.
func decodeEnvelope(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSON(w, http.StatusOK, map[string]string{"ok": "yes"})

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "yes", body["ok"])
}

func TestSuccessEnvelope(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/users", nil)

	Success(w, r, http.StatusOK, map[string]string{"name": "alice"})

	assert.Equal(t, http.StatusOK, w.Code)

	body := decodeEnvelope(t, w)
	assert.Equal(t, "success", body["status"])
	assert.NotContains(t, body, "message")
	assert.NotContains(t, body, "error")
	assert.NotContains(t, body, "links")

	data, ok := body["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "alice", data["name"])

	meta, ok := body["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(http.StatusOK), meta["status_code"])
	assert.NotEmpty(t, meta["request_id"])
	assert.NotContains(t, meta, "trace_id")
	assert.NotContains(t, meta, "page")
}

func TestSuccessEchoesRequestID(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	r.Header.Set("X-Request-Id", "req-123")

	Success(w, r, http.StatusOK, nil)

	assert.Equal(t, "req-123", w.Header().Get("X-Request-Id"))
	meta := decodeEnvelope(t, w)["metadata"].(map[string]any)
	assert.Equal(t, "req-123", meta["request_id"])
}

func TestSuccessGeneratesRequestID(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/users", nil)

	Success(w, r, http.StatusOK, nil)

	id := w.Header().Get("X-Request-Id")
	assert.True(t, strings.HasPrefix(id, "req_"), id)
	assert.Len(t, id, len("req_")+26)
	meta := decodeEnvelope(t, w)["metadata"].(map[string]any)
	assert.Equal(t, id, meta["request_id"])
}

func TestSuccessWithTraceIDAndRateLimitHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	r.Header.Set("X-Trace-Id", "trace-abc")
	w.Header().Set("X-RateLimit-Limit", "100")
	w.Header().Set("X-RateLimit-Remaining", "42")
	w.Header().Set("X-RateLimit-Reset", "1700000000")

	Success(w, r, http.StatusOK, nil)

	meta := decodeEnvelope(t, w)["metadata"].(map[string]any)
	assert.Equal(t, "trace-abc", meta["trace_id"])
	assert.Equal(t, float64(100), meta["rate_limit"].(map[string]any)["limit"])
	assert.Equal(t, float64(42), meta["rate_limit"].(map[string]any)["remaining"])
	assert.Equal(t, float64(1700000000), meta["rate_limit"].(map[string]any)["reset"])
}

func TestSuccessWithPagination(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/users", nil)

	Success(w, r, http.StatusCreated, nil,
		WithPagination(NewPagination(PaginationParams{Page: 2, Limit: 10}, 35)),
	)

	body := decodeEnvelope(t, w)

	meta := body["metadata"].(map[string]any)
	assert.Equal(t, float64(2), meta["page"])
	assert.Equal(t, float64(10), meta["limit"])
	assert.Equal(t, float64(4), meta["total_pages"])
	assert.Equal(t, float64(35), meta["total_items"])
	assert.Equal(t, float64(10), meta["first_item_index"])
	assert.Equal(t, float64(19), meta["last_item_index"])
}

func TestFailEnvelope(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/users", nil)

	Fail(w, r, http.StatusUnprocessableEntity, "validation failed",
		WithError(map[string]any{"field": "email", "reason": "invalid format"}))

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	body := decodeEnvelope(t, w)
	assert.Equal(t, "error", body["status"])
	assert.Equal(t, "validation failed", body["message"])
	assert.NotContains(t, body, "data")

	errDetail := body["error"].(map[string]any)
	assert.Equal(t, "email", errDetail["field"])

	meta := body["metadata"].(map[string]any)
	assert.Equal(t, float64(http.StatusUnprocessableEntity), meta["status_code"])
	assert.NotEmpty(t, meta["request_id"])
}

func TestFailWithoutErrorDetail(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/users", nil)

	Fail(w, r, http.StatusInternalServerError, "internal server error")

	body := decodeEnvelope(t, w)
	assert.NotContains(t, body, "error")
	assert.Equal(t, "internal server error", body["message"])
}

func TestNotFoundJSON(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	NotFoundJSON(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	body := decodeEnvelope(t, w)
	assert.Equal(t, "error", body["status"])
	assert.Equal(t, "not found", body["message"])
}

func TestMethodNotAllowedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/users", nil)
	MethodNotAllowedJSON(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)

	body := decodeEnvelope(t, w)
	assert.Equal(t, "error", body["status"])
	assert.Equal(t, "method not allowed", body["message"])
}

// TestWriteErrorNamesTheCauseInTheLog pins the one place an unexpected
// failure becomes a response: the client is told nothing, the operator is
// told everything. Without the log line a 500 in the record names a status
// and no cause.
func TestWriteErrorNamesTheCauseInTheLog(t *testing.T) {
	var captured bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&captured, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/users/01a0/profile-picture.png", nil)
	req = req.WithContext(WithRequestID(req.Context(), "req_01m3dfts39e58b4zbtqqyq6aey"))

	WriteError(w, req, errors.New("storage: not found: pictures/01a0.png"))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	body := decodeEnvelope(t, w)
	assert.Equal(t, "internal error", body["message"])
	assert.NotContains(t, w.Body.String(), "storage", "the cause never reaches the client")

	line := captured.String()
	assert.Contains(t, line, "storage: not found: pictures/01a0.png")
	assert.Contains(t, line, "req_01m3dfts39e58b4zbtqqyq6aey")
	assert.Contains(t, line, "/api/users/01a0/profile-picture.png")
}

// TestWriteErrorKeepsValidationErrorsInTheResponse is the other half: a
// failure the caller can fix travels to the caller, and is not logged as an
// internal one.
func TestWriteErrorKeepsValidationErrorsInTheResponse(t *testing.T) {
	var captured bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&captured, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/users", nil)

	WriteError(w, req, validation.Errors{"email": errors.New("is required")})

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Empty(t, captured.String(), "a caller's mistake is not an operator's log line")
}

func TestBadRequestJSON(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/users", nil)
	BadRequestJSON(w, req, "name is required")

	assert.Equal(t, http.StatusBadRequest, w.Code)

	body := decodeEnvelope(t, w)
	assert.Equal(t, "error", body["status"])
	assert.Equal(t, "name is required", body["message"])
}
