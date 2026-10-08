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
	req = req.WithContext(webutil.WithRequestID(req.Context(), "req_abc123"))
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

func TestLoggerQuietPathsWriteNoLine(t *testing.T) {
	var buf bytes.Buffer
	quiet := []string{"/favicon.", "/@fs/", "/@react-refresh", "/src/"}
	handler := Logger(slog.New(slog.NewJSONHandler(&buf, nil)), quiet...)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	for _, path := range []string{
		"/favicon.ico", // the prefix matches without a slash boundary
		"/favicon.svg",
		"/@fs/etc/passwd",
		"/@react-refresh", // the bare prefix names itself
		"/src/main.tsx",
		"/src/routes/(auth)/login.tsx",
	} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	assert.Empty(t, strings.TrimSpace(buf.String()), "a quiet path's success answers no line")

	// A 4xx a quiet path earned is the browser's normal state — a stale HMR
	// probe, a devtools manifest that is not there — and stays silent too.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/src/ghost.ts", nil))
	assert.Empty(t, strings.TrimSpace(buf.String()), "a quiet path's 4xx answers no line")

	// A neighbouring path the prefixes do not name still logs.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/things", nil))
	assert.NotEmpty(t, strings.TrimSpace(buf.String()))
	assert.Equal(t, "/api/things", requestLog(t, &buf)["path"])
}

func TestLoggerAQuietPathThatFailsStillLogs(t *testing.T) {
	var buf bytes.Buffer
	handler := Logger(slog.New(slog.NewJSONHandler(&buf, nil)), "/src/")(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/src/main.tsx", nil))

	entry := requestLog(t, &buf)
	assert.Equal(t, slog.LevelError.String(), entry["level"])
	assert.Equal(t, "/src/main.tsx", entry["path"])
}
