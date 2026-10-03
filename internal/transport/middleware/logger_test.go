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

	"github.com/riipandi/saka/pkg/responder"
)

// requestLog reads the logged line as a map, so an assertion names a field
// rather than parsing text in the test body.
func requestLog(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	var entry map[string]any
	line := strings.TrimSpace(buf.String())
	require.NotEmpty(t, line, "a request must be logged")
	require.NoError(t, json.Unmarshal([]byte(line), &entry), "the line must be one JSON object: %s", line)
	return entry
}

func TestLoggerWritesOneLinePerRequest(t *testing.T) {
	var buf bytes.Buffer
	handler := Logger(slog.New(slog.NewJSONHandler(&buf, nil)))(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))

	req := httptest.NewRequest(http.MethodGet, "/api/things", nil)
	req = req.WithContext(responder.WithRequestID(req.Context(), "req_abc123"))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	entry := requestLog(t, &buf)
	assert.Equal(t, "WARN", entry["level"], "a 4xx status is a warning")
	assert.Equal(t, "req_abc123", entry["request_id"])
	assert.Equal(t, "GET", entry["method"])
	assert.Equal(t, "/api/things", entry["path"])
	assert.Equal(t, float64(418), entry["status"])
	assert.NotEmpty(t, entry["duration"])
}

func TestLoggerFollowsTheStatusLevel(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusOK:                  "INFO",
		http.StatusInternalServerError: "ERROR",
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var buf bytes.Buffer
			handler := Logger(slog.New(slog.NewJSONHandler(&buf, nil)))(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(status)
				}))

			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

			assert.Equal(t, want, requestLog(t, &buf)["level"])
		})
	}
}

func TestLoggerCountsTheBytesItServed(t *testing.T) {
	var buf bytes.Buffer
	handler := Logger(slog.New(slog.NewJSONHandler(&buf, nil)))(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("0123456789"))
		}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, float64(10), requestLog(t, &buf)["bytes"])
}

func TestLoggerWithoutALoggerIsAPassThrough(t *testing.T) {
	handler := Logger(nil)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusAccepted, rec.Code)
}
