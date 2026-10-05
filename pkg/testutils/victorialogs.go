package testutils

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// VictoriaLogs is a running log store container.
type VictoriaLogs struct {
	// Endpoint is the base URL (http://host:port), which is what a Perses
	// datasource and the query API both take.
	Endpoint string

	// OTLPEndpoint is the address the application ships to. It is the full
	// ingestion path rather than the base URL: VictoriaLogs serves OTLP under
	// /insert/opentelemetry/v1/logs, not under the protocol's own /v1/logs.
	OTLPEndpoint string

	container *tc.DockerContainer
}

// victoriaLogsImage is the log store used by integration tests. It is the same
// store the development compose stack runs, so a test exercises what a developer
// reads in Perses.
const victoriaLogsImage = "docker.io/victoriametrics/victoria-logs:v1.50.0"

// victoriaLogsPort is the HTTP port inside the container: the query API and the
// OTLP ingestion path are both served on it.
const victoriaLogsPort = "9428"

var (
	victoriaLogsOnce   sync.Once
	sharedVictoriaLogs *VictoriaLogs
	victoriaLogsErr    error
)

// StartVictoriaLogs returns the shared VictoriaLogs container for the test
// binary.
//
// One container is shared because the query API is global: every test that
// ships a line can read it back, and the tests are written to look for their own
// marker rather than for an empty store. Starting one per test would cost a
// container each and buy nothing.
func StartVictoriaLogs(ctx context.Context, t testing.TB) *VictoriaLogs {
	t.Helper()

	SkipWithoutDocker(t)

	victoriaLogsOnce.Do(func() {
		sharedVictoriaLogs, victoriaLogsErr = startVictoriaLogs(ctx)
	})

	require.NoError(t, victoriaLogsErr, "start victoria-logs container (docker daemon required)")
	return sharedVictoriaLogs
}

// startVictoriaLogs starts the container and waits for the store to answer.
//
// The wait is an HTTP request rather than a port check: the port is listening
// before the store can ingest, and a test that shipped to a half-started store
// would fail on a line that was never written.
func startVictoriaLogs(ctx context.Context) (*VictoriaLogs, error) {
	container, err := tc.Run(ctx, victoriaLogsImage,
		tc.WithExposedPorts(victoriaLogsPort),
		tc.WithCmd("-storageDataPath=/victoria-logs-data"),
		tc.WithWaitStrategy(wait.ForHTTP("/health").WithPort(victoriaLogsPort).
			WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		return nil, fmt.Errorf("run victoria-logs container: %w", err)
	}

	endpoint, err := container.PortEndpoint(ctx, victoriaLogsPort, "http")
	if err != nil {
		return nil, fmt.Errorf("resolve victoria-logs endpoint: %w", err)
	}
	return &VictoriaLogs{
		Endpoint:     endpoint,
		OTLPEndpoint: endpoint + "/insert/opentelemetry/v1/logs",
		container:    container,
	}, nil
}

// Query runs a LogsQL query and returns the matching lines, one per result. It
// fails the test on a transport error, because a query that could not run is a
// broken test rather than a missing log line.
func (v *VictoriaLogs) Query(ctx context.Context, t testing.TB, query string) []string {
	t.Helper()

	form := url.Values{"query": {query}, "limit": {"100"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		v.Endpoint+"/select/logsql/query", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err, "query victoria-logs")
	defer func() { _ = response.Body.Close() }()

	require.Equal(t, http.StatusOK, response.StatusCode, "victoria-logs rejected the query")

	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// AwaitQuery runs a query until it matches, and returns the lines it found.
//
// Shipping is batched, so a line that was just emitted is not queryable
// immediately. Waiting for it is what makes the assertion about the pipeline
// rather than about a sleep: the alternative is a fixed delay that is either
// flaky or slow.
func (v *VictoriaLogs) AwaitQuery(ctx context.Context, t testing.TB, query string) []string {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for {
		if lines := v.Query(ctx, t, query); len(lines) > 0 {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("no log line matched %q within the deadline", query)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
