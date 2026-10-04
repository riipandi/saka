package health_test

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/health"
)

// nonEmptyLines splits output into lines, dropping blank ones.
func nonEmptyLines(s string) []string {
	var lines []string
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// getHealth calls the handler and returns the response and its decoded body.
func getHealth(t *testing.T, checker *health.Checker) (*http.Response, map[string]any) {
	t.Helper()

	recorder := httptest.NewRecorder()
	health.Handler(checker).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))

	response := recorder.Result()
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })

	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return response, body
}

// TestHandlerPublishesNoTarget is the disclosure rule at the surface that
// matters: the endpoint is unauthenticated, so its report must not name the
// host of a dependency. The checks are built the way the registry builds
// them, so a regression in the wiring is caught here too.
func TestHandlerPublishesNoTarget(t *testing.T) {
	checker := health.NewChecker(
		health.WithCheck(health.DatabaseCheck(&stubPinger{})),
		health.WithCheck(health.KVStoreCheck(&stubPinger{})),
	)

	_, body := getHealth(t, checker)

	data, ok := body["data"].(map[string]any)
	require.True(t, ok, "the envelope must carry a data object")

	details, ok := data["details"].([]any)
	require.True(t, ok, "the data object must carry details")

	for _, entry := range details {
		detail, ok := entry.(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, detail, "target",
			"the published report must not name the host of %v", detail["name"])
	}
}

// stubPinger answers a successful probe for the checks above.
type stubPinger struct{}

func (stubPinger) Ping(context.Context) error { return nil }

func TestHandlerAnswers200WhenHealthy(t *testing.T) {
	checker := health.NewChecker(health.WithCheck(health.Check{Name: "postgres", Check: passing}))

	response, body := getHealth(t, checker)

	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "success", body["status"])
	assert.Contains(t, body, "data")
	assert.Contains(t, body, "metadata")

	// The payload reports the global status, which is a different vocabulary
	// from a component: the system is healthy, a component is up.
	data, ok := body["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "healthy", data["status"])

	// The body must be the standard envelope, so a client parses it the same
	// way it parses every other response.
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "metadata must be an object")
	assert.Equal(t, float64(http.StatusOK), metadata["status_code"])
	assert.NotEmpty(t, metadata["request_id"])
}

// A failing check must answer 503, so a load balancer can act on the status
// code alone without parsing the body.
func TestHandlerAnswers503WhenDown(t *testing.T) {
	checker := health.NewChecker(health.WithCheck(health.Check{Name: "postgres", Check: failing}))

	response, body := getHealth(t, checker)

	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	assert.Equal(t, "error", body["status"])

	// The message names the failing check, so a reader knows what to look at
	// without opening the details. It is the same one-line message the CLI
	// prints, so the two surfaces describe a failure the same way.
	message, ok := body["message"].(string)
	require.True(t, ok, "message must be a string")
	assert.Contains(t, message, "unhealthy")
	assert.Contains(t, message, "postgres")

	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(http.StatusServiceUnavailable), metadata["status_code"])
}

// The failing detail must reach the response, so a client can show why.
func TestHandlerIncludesFailureDetail(t *testing.T) {
	checker := health.NewChecker(health.WithCheck(health.Check{Name: "postgres", Check: failing}))

	_, body := getHealth(t, checker)

	detail, ok := body["error"].(map[string]any)
	require.True(t, ok, "error must carry the result object")
	assert.Equal(t, "unhealthy", detail["status"])

	postgres := detailFor(t, detail, "postgres")
	assert.Equal(t, "down", postgres["status"])
	assert.Equal(t, "boom", postgres["error"])
	assert.NotEmpty(t, postgres["timestamp"])

	// Durations are published in milliseconds, not nanoseconds: a probe result
	// is meant to be read.
	assert.Contains(t, postgres, "took_ms")
	assert.NotContains(t, postgres, "duration")
}

// detailFor finds one check in a decoded result. Details are an ordered array,
// not a map, so a diff of two responses does not churn.
func detailFor(t *testing.T, result map[string]any, name string) map[string]any {
	t.Helper()

	details, ok := result["details"].([]any)
	require.True(t, ok, "details must be an array")
	for _, entry := range details {
		detail, ok := entry.(map[string]any)
		require.True(t, ok, "each detail must be an object")
		if detail["name"] == name {
			return detail
		}
	}
	t.Fatalf("check %q missing from details %v", name, details)
	return nil
}

// The details array must keep a stable order, so the same state always produces
// the same body.
func TestHandlerOrdersDetailsByName(t *testing.T) {
	checker := health.NewChecker(health.WithChecks(
		health.Check{Name: "valkey", Check: passing},
		health.Check{Name: "postgres", Check: passing},
	))

	_, body := getHealth(t, checker)

	details, ok := body["data"].(map[string]any)["details"].([]any)
	require.True(t, ok, "details must be an array")

	var names []string
	for _, entry := range details {
		detail, ok := entry.(map[string]any)
		require.True(t, ok)
		names = append(names, detail["name"].(string))
	}
	assert.Equal(t, []string{"postgres", "valkey"}, names)
}

// The info entries identify the build that answered the probe. They are
// flattened to the top level of the response, not nested under an object.
func TestHandlerPublishesInfo(t *testing.T) {
	checker := health.NewChecker(
		health.WithCheck(health.Check{Name: "postgres", Check: passing}),
		health.WithInfo(map[string]string{"name": "hogwarts", "version": "1.2.3"}),
	)

	_, body := getHealth(t, checker)

	data, ok := body["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "hogwarts", data["name"])
	assert.Equal(t, "1.2.3", data["version"])
	assert.NotContains(t, data, "info", "info entries must be flattened, not nested")
}

// An optional dependency that is down must not fail the endpoint, or a missing
// optional backend would take the service out of rotation.
func TestHandlerIgnoresOptionalFailure(t *testing.T) {
	checker := health.NewChecker(health.WithChecks(
		health.Check{Name: "postgres", Check: passing},
		health.Check{Name: "valkey", Check: failing, Optional: true},
	))

	response, body := getHealth(t, checker)

	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "success", body["status"])
}

// A cached health response would keep reporting a stale status, so the handler
// must mark it uncacheable.
func TestHandlerDisablesCaching(t *testing.T) {
	checker := health.NewChecker(health.WithCheck(health.Check{Name: "postgres", Check: passing}))

	response, _ := getHealth(t, checker)

	assert.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	assert.Equal(t, "no-cache", response.Header.Get("Pragma"))
	assert.Equal(t, "0", response.Header.Get("Expires"))
}

// The handler must serve concurrent probes without racing on the cached state.
func TestHandlerServesConcurrentRequests(t *testing.T) {
	checker := health.NewChecker(
		health.WithCacheTTL(0),
		health.WithCheck(health.Check{Name: "postgres", Check: passing}),
	)
	handler := health.Handler(checker)

	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 10 {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
				assert.Equal(t, http.StatusOK, recorder.Code)
			}
		}()
	}
	for range 8 {
		<-done
	}
}

func TestMessageNamesFailingChecks(t *testing.T) {
	result := health.Result{
		Status: health.GlobalUnhealthy,
		Details: map[string]health.CheckResult{
			"postgres": {Name: "postgres", Status: health.StatusDown, Error: "boom"},
			"valkey":   {Name: "valkey", Status: health.StatusDown, Error: "boom"},
		},
	}

	assert.Equal(t, "unhealthy: postgres, valkey is down", health.Message(result))
}

func TestMessageReportsHealthyStatus(t *testing.T) {
	result := health.Result{Status: health.GlobalHealthy, Details: map[string]health.CheckResult{}}

	assert.Equal(t, "healthy (0 s)", health.Message(result))
}

// WriteText is the CLI's default output, so its shape is asserted exactly: a
// stable shape is what makes the output parseable by eye and by a log reader.
func TestWriteTextHealthyReport(t *testing.T) {
	result := health.Result{
		Status:   health.GlobalHealthy,
		Duration: 1500 * time.Millisecond,
		Details: map[string]health.CheckResult{
			"postgres": {
				Name:      "postgres",
				Target:    "localhost:5432/postgres",
				Status:    health.StatusUp,
				Timestamp: time.Now().Add(-2 * time.Second),
				Duration:  235 * time.Microsecond,
			},
			"storage": {Name: "storage", Target: "/srv/storage", Status: health.StatusUp},
		},
		Info: map[string]string{"name": "hogwarts", "version": "1.2.3", "uptime": "3 hours"},
	}

	var out strings.Builder
	require.NoError(t, health.WriteText(&out, result, nil))

	assert.Equal(t, `name: hogwarts
uptime: 3 hours
version: 1.2.3
status: healthy
duration: 1.5 s
checks: 2 up, 0 down
postgres: up (localhost:5432/postgres)
storage: up (/srv/storage)
`, out.String())
}

// A failing check must carry its error on its own line, and the summary must
// count it, so `grep ': down'` finds every problem without extra tooling.
func TestWriteTextFailingReport(t *testing.T) {
	result := health.Result{
		Status: health.GlobalUnhealthy,
		Details: map[string]health.CheckResult{
			"postgres": {
				Name:     "postgres",
				Target:   "localhost:5432/postgres",
				Status:   health.StatusDown,
				Error:    "connection refused",
				Duration: 2 * time.Millisecond,
			},
			"valkey": {Name: "valkey", Status: health.StatusUp, Optional: true},
		},
	}

	var out strings.Builder
	require.NoError(t, health.WriteText(&out, result, nil))

	assert.Contains(t, out.String(), "status: unhealthy")
	assert.Contains(t, out.String(), "checks: 1 up, 1 down, 1 optional")
	assert.Contains(t, out.String(), "postgres: down (localhost:5432/postgres): connection refused")
	assert.Contains(t, out.String(), "valkey: up optional")
}

// Every line must be "<name>: <status>...", so a reader or a script can split on
// the first ": " without knowing which check it is looking at.
func TestWriteTextCheckLinesAreParseable(t *testing.T) {
	result := health.Result{
		Status: health.GlobalUnhealthy,
		Details: map[string]health.CheckResult{
			"postgres": {Name: "postgres", Status: health.StatusDown, Error: "boom"},
			"storage":  {Name: "storage", Target: "/srv/storage", Status: health.StatusUp},
			"valkey":   {Name: "valkey", Status: health.StatusUp, Optional: true},
		},
	}

	var out strings.Builder
	require.NoError(t, health.WriteText(&out, result, nil))

	for _, line := range checkLines(t, out.String()) {
		name, rest, found := strings.Cut(line, ": ")
		require.True(t, found, "check line must be name: status: %q", line)
		assert.Contains(t, result.Details, name, "line must start with a check name: %q", line)
		assert.True(t,
			strings.HasPrefix(rest, "up") || strings.HasPrefix(rest, "down"),
			"line must carry a status right after the name: %q", line)
	}
}

// checkLines returns the check lines of a report: everything after the blank
// line that separates the summary from the checks.
func checkLines(t *testing.T, report string) []string {
	t.Helper()

	lines := nonEmptyLines(report)
	for i, line := range lines {
		if strings.HasPrefix(line, "checks: ") {
			require.Less(t, i+1, len(lines), "report has no check lines:\n%s", report)
			return lines[i+1:]
		}
	}
	t.Fatalf("report has no checks summary:\n%s", report)
	return nil
}

// Output must not carry trailing spaces: they are noise in a diff and in a
// copied line.
func TestWriteTextHasNoTrailingWhitespace(t *testing.T) {
	result := health.Result{
		Status: health.GlobalHealthy,
		Details: map[string]health.CheckResult{
			"a-short":           {Name: "a-short", Status: health.StatusUp},
			"a-much-longer-one": {Name: "a-much-longer-one", Status: health.StatusUp, Target: "/x"},
		},
	}

	var out strings.Builder
	require.NoError(t, health.WriteText(&out, result, nil))

	for line := range strings.SplitSeq(out.String(), "\n") {
		assert.Equal(t, strings.TrimRight(line, " \t"), line, "line has trailing whitespace: %q", line)
	}
}

// A result with no checks must still print a summary, so an empty checker does
// not produce an empty report or a dangling blank line.
func TestWriteTextWithoutChecks(t *testing.T) {
	var out strings.Builder
	require.NoError(t, health.WriteText(&out, health.Result{Status: health.GlobalHealthy}, nil))

	assert.Equal(t, "status: healthy\nduration: 0 s\nchecks: 0 up, 0 down\n", out.String())
}

// Check lines follow the summary directly: no blank line between them, so the
// report is a flat list a script can read line by line.
func TestWriteTextHasNoBlankLineBeforeChecks(t *testing.T) {
	result := health.Result{
		Status: health.GlobalHealthy,
		Details: map[string]health.CheckResult{
			"postgres": {Name: "postgres", Status: health.StatusUp},
		},
	}

	var out strings.Builder
	require.NoError(t, health.WriteText(&out, result, nil))

	assert.Equal(t, "status: healthy\nduration: 0 s\nchecks: 1 up, 0 down\npostgres: up\n", out.String())
	assert.NotContains(t, out.String(), "\n\n")
}

// The whole-call duration must be humanized, not raw nanoseconds: "1.5 s" is
// readable where "1500000000" is not.
func TestWriteTextHumanizesDurations(t *testing.T) {
	result := health.Result{
		Status:   health.GlobalHealthy,
		Duration: 1500 * time.Millisecond,
	}

	var out strings.Builder
	require.NoError(t, health.WriteText(&out, result, nil))

	assert.Contains(t, out.String(), "duration: 1.5 s")
}

// Per-check durations and timestamps are absent from the text report: they are
// per-run numbers that answer no question a reader has. They stay in JSON.
func TestWriteTextOmitsPerCheckTiming(t *testing.T) {
	result := health.Result{
		Status: health.GlobalHealthy,
		Details: map[string]health.CheckResult{
			"postgres": {
				Name:      "postgres",
				Status:    health.StatusUp,
				Duration:  235 * time.Microsecond,
				Timestamp: time.Now().Add(-2 * time.Hour),
			},
		},
	}

	var out strings.Builder
	require.NoError(t, health.WriteText(&out, result, nil))

	assert.NotContains(t, out.String(), "235 µs")
	assert.NotContains(t, out.String(), "hours ago")
	assert.Equal(t, "postgres: up\n", checkLines(t, out.String())[0]+"\n")
}

// WriteShort is the --short output: one word, so a shell script reads it without
// parsing anything.
func TestWriteShortPrintsOnlyTheStatus(t *testing.T) {
	tests := []struct {
		status health.GlobalStatus
		want   string
	}{
		{status: health.GlobalHealthy, want: "healthy\n"},
		{status: health.GlobalUnhealthy, want: "unhealthy\n"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			var out strings.Builder
			require.NoError(t, health.WriteShort(&out, health.Result{
				Status: tt.status,
				Details: map[string]health.CheckResult{
					"postgres": {Name: "postgres", Status: health.StatusDown, Error: "boom"},
				},
			}))

			assert.Equal(t, tt.want, out.String())
		})
	}
}

// WithInfoFunc values are computed on every call, which is what uptime needs:
// the same checker reports a growing value.
func TestInfoFuncIsEvaluatedPerCall(t *testing.T) {
	var calls int
	checker := health.NewChecker(
		health.WithCheck(health.Check{Name: "a", Check: passing}),
		health.WithInfo(map[string]string{"version": "1.2.3"}),
		health.WithInfoFunc(func(context.Context) map[string]string {
			calls++
			return map[string]string{"uptime": fmt.Sprintf("call %d", calls)}
		}),
	)

	first := checker.Check(t.Context())
	assert.Equal(t, "call 1", first.Info["uptime"])
	assert.Equal(t, "1.2.3", first.Info["version"])

	second := checker.Check(t.Context())
	assert.Equal(t, "call 2", second.Info["uptime"], "the info function must run again")
}

// A static key outranks a computed one, so a build fact cannot be shadowed by a
// derived value that happens to share its name.
func TestInfoStaticValueWinsOverFunc(t *testing.T) {
	checker := health.NewChecker(
		health.WithCheck(health.Check{Name: "a", Check: passing}),
		health.WithInfo(map[string]string{"version": "static"}),
		health.WithInfoFunc(func(context.Context) map[string]string {
			return map[string]string{"version": "computed"}
		}),
	)

	assert.Equal(t, "static", checker.Check(t.Context()).Info["version"])
}

// Uptime must grow with the elapsed time and stay readable.
func TestUptimeReportsElapsedTime(t *testing.T) {
	tests := []struct {
		name    string
		elapsed time.Duration
		want    string
	}{
		// go-humanize has no sub-minute granularity, so "now" would look like a
		// missing value rather than a short uptime.
		{name: "just started", elapsed: 250 * time.Millisecond, want: "<1 minute"},
		{name: "seconds", elapsed: 5 * time.Second, want: "<1 minute"},
		{name: "minutes", elapsed: 90 * time.Second, want: "1 minute"},
		{name: "hours", elapsed: 3 * time.Hour, want: "3 hours"},
		{name: "days", elapsed: 26 * time.Hour, want: "1 day"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := health.Uptime(time.Now().Add(-tt.elapsed))(t.Context())
			assert.Equal(t, tt.want, info[health.InfoUptime])
		})
	}
}

// The JSON body must be byte-identical for the same state: encoding/json/v2
// does not sort map keys, so a map field would churn between runs.
func TestMarshalIsDeterministic(t *testing.T) {
	result := health.Result{
		Status:   health.GlobalHealthy,
		Duration: time.Millisecond,
		Details: map[string]health.CheckResult{
			"storage":  {Name: "storage", Status: health.StatusUp},
			"postgres": {Name: "postgres", Status: health.StatusUp},
		},
		Info: map[string]string{"version": "1", "name": "hogwarts", "uptime": "<1 minute"},
	}

	encoded, err := json.Marshal(result)
	require.NoError(t, err)

	// Twenty runs is enough to trip over Go's randomized map order.
	for range 20 {
		again, err := json.Marshal(result)
		require.NoError(t, err)
		assert.Equal(t, string(encoded), string(again))
	}

	// The info entries are flattened to the top level, after the fixed
	// fields, in key order.
	assert.Contains(t, string(encoded), `"took_ms":1`)
	assert.Less(t, strings.Index(string(encoded), `"postgres"`), strings.Index(string(encoded), `"storage"`))
	assert.Less(t, strings.Index(string(encoded), `"took_ms":1`), strings.Index(string(encoded), `"name":"hogwarts"`))
	assert.Less(t, strings.Index(string(encoded), `"name":"hogwarts"`), strings.Index(string(encoded), `"uptime"`))
	assert.Less(t, strings.Index(string(encoded), `"uptime"`), strings.Index(string(encoded), `"version"`))
}
