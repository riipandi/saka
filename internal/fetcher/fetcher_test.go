package fetcher

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
)

func TestShutdownSatisfiesTheContainer(t *testing.T) {
	var _ do.ShutdownerWithContextAndError = (*Client)(nil)
}

func TestDoSucceeds(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("User-Agent")
		w.Header().Set("X-Test", "yes")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	client := newClient(t, config.Default())
	res, err := client.Do(t.Context(), Request{URL: server.URL + "/health"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, `{"ok":true}`, string(res.Body))
	assert.Equal(t, "yes", res.Header.Get("X-Test"))
	assert.Equal(t, config.DefaultUserAgent(), seen)
	assert.Equal(t, 1, res.Attempts)
}

func TestDoSendsHeadersQueryAndBody(t *testing.T) {
	var method, query, auth, body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		query = r.URL.Query().Get("q")
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))
	t.Cleanup(server.Close)

	client := newClient(t, config.Default())
	res, err := client.Do(t.Context(), Request{
		Method: http.MethodPut,
		URL:    server.URL + "/widgets",
		Query:  url.Values{"q": {"bolt"}},
		Headers: http.Header{
			"Authorization": []string{"Bearer super-secret-token"},
			"Accept":        []string{"application/json"},
		},
		Body: map[string]string{"password": "hunter2"},
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, res.StatusCode)
	assert.Equal(t, http.MethodPut, method)
	assert.Equal(t, "bolt", query)
	assert.Equal(t, "Bearer super-secret-token", auth)
	assert.Contains(t, body, "hunter2")
	assert.NotContains(t, res.Header.Get("User-Agent"), "Mozilla")
}

func TestDoReportsClientAndServerStatuses(t *testing.T) {
	status := http.StatusNotFound
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "secret-body", status)
	}))
	t.Cleanup(server.Close)

	client := newClient(t, fastRetries(0))
	res, err := client.Do(t.Context(), Request{URL: server.URL})
	require.ErrorIs(t, err, ErrStatus)
	assert.NotErrorIs(t, err, ErrRetryExhausted)
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
	assert.Contains(t, string(res.Body), "secret-body")
	assert.NotContains(t, err.Error(), "secret-body")
	assert.Equal(t, int32(1), hits.Load())

	status = http.StatusBadGateway
	hits.Store(0)
	res, err = client.Do(t.Context(), Request{URL: server.URL})
	require.ErrorIs(t, err, ErrStatus)
	assert.Equal(t, http.StatusBadGateway, res.StatusCode)
	assert.Equal(t, int32(1), hits.Load())
	var failed *Error
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, http.StatusBadGateway, failed.Status)
}

func TestDoDoesNotRetryMostClientErrorsOrAPost(t *testing.T) {
	status := http.StatusBadRequest
	method := ""
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		method = r.Method
		http.Error(w, "no", status)
	}))
	t.Cleanup(server.Close)

	client := newClient(t, fastRetries(2))
	_, err := client.Do(t.Context(), Request{URL: server.URL})
	require.ErrorIs(t, err, ErrStatus)
	assert.NotErrorIs(t, err, ErrRetryExhausted)
	assert.Equal(t, int32(1), hits.Load())

	status = http.StatusBadGateway
	hits.Store(0)
	_, err = client.Do(t.Context(), Request{
		Method: http.MethodPost,
		URL:    server.URL,
		Body:   map[string]string{"n": "1"},
	})
	require.ErrorIs(t, err, ErrStatus)
	assert.NotErrorIs(t, err, ErrRetryExhausted)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, int32(1), hits.Load())
}

func TestDoRetriesATransientStatusThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			http.Error(w, "later", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)

	client := newClient(t, fastRetries(2))
	res, err := client.Do(t.Context(), Request{URL: server.URL})
	require.NoError(t, err)
	assert.Equal(t, "ok", string(res.Body))
	assert.Equal(t, 3, res.Attempts)
	assert.Equal(t, int32(3), hits.Load())
}

func TestDoReportsRetryExhaustion(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	client := newClient(t, fastRetries(2))
	res, err := client.Do(t.Context(), Request{URL: server.URL})
	require.ErrorIs(t, err, ErrRetryExhausted)
	require.ErrorIs(t, err, ErrStatus)
	assert.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
	assert.Equal(t, 3, res.Attempts)
	assert.Equal(t, int32(3), hits.Load())
}

func TestDoReportsANetworkErrorAndRetriesIt(t *testing.T) {
	// A port that just closed refuses the connection, so the attempt ends
	// without waiting out the timeout.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	client := newClient(t, fastRetries(1))
	_, err = client.Do(t.Context(), Request{URL: "http://" + addr + "/"})
	require.ErrorIs(t, err, ErrNetwork)
	assert.NotErrorIs(t, err, ErrTimeout)
	require.ErrorIs(t, err, ErrRetryExhausted)
	var failed *Error
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, 2, failed.Attempts)
	assert.NotContains(t, err.Error(), "?")
}

func TestDoReportsATimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second)
		_, _ = w.Write([]byte("late"))
	}))
	t.Cleanup(server.Close)

	cfg := fastRetries(0)
	cfg.Fetcher.Timeout = 50 * time.Millisecond
	cfg.Fetcher.CircuitResetTimeout = time.Second
	client := newClient(t, cfg)
	_, err := client.Do(t.Context(), Request{URL: server.URL})
	require.ErrorIs(t, err, ErrTimeout)
	assert.NotErrorIs(t, err, ErrRetryExhausted)
	assert.NotErrorIs(t, err, ErrCanceled)
}

func TestDoStopsWhenTheContextIsCanceled(t *testing.T) {
	started := make(chan struct{})
	var hits atomic.Int32
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		once.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	cfg := fastRetries(2)
	cfg.Fetcher.Timeout = 5 * time.Second
	cfg.Fetcher.RetryWait = time.Second
	cfg.Fetcher.RetryMaxWait = time.Second
	cfg.Fetcher.CircuitResetTimeout = 5 * time.Second
	client := newClient(t, cfg)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		<-started
		cancel()
	}()
	_, err := client.Do(ctx, Request{URL: server.URL})
	require.ErrorIs(t, err, ErrCanceled)
	assert.NotErrorIs(t, err, ErrRetryExhausted)
	assert.LessOrEqual(t, hits.Load(), int32(1))
}

func TestDoOpensTheCircuitBreaker(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	cfg := fastRetries(0)
	cfg.Fetcher.CircuitFailureThreshold = 2
	cfg.Fetcher.CircuitSuccessThreshold = 1
	cfg.Fetcher.CircuitResetTimeout = time.Hour
	client := newClient(t, cfg)

	_, err := client.Do(t.Context(), Request{URL: server.URL})
	require.ErrorIs(t, err, ErrStatus)
	_, err = client.Do(t.Context(), Request{URL: server.URL})
	require.ErrorIs(t, err, ErrStatus)
	_, err = client.Do(t.Context(), Request{URL: server.URL})
	require.ErrorIs(t, err, ErrCircuitOpen)
	assert.NotErrorIs(t, err, ErrStatus)
	assert.Equal(t, int32(2), hits.Load())
}

func TestLogsDoNotCarrySecrets(t *testing.T) {
	records := &logCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"password":"hunter2"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	client := newClientWithLog(t, fastRetries(0), slog.New(records))
	_, err := client.Do(t.Context(), Request{
		URL: server.URL + "/login?token=super-secret-query",
		Headers: http.Header{
			"Authorization": []string{"Bearer super-secret-token"},
		},
		Body: map[string]string{"password": "hunter2"},
	})
	require.Error(t, err)

	slogAdapter{log: slog.New(records)}.Errorf("authorization: Bearer super-secret-token password=hunter2 https://user:pw@example.com/path?token=super-secret-query")
	text := records.joined()
	assert.NotContains(t, text, "super-secret-token")
	assert.NotContains(t, text, "hunter2")
	assert.NotContains(t, text, "super-secret-query")
	assert.NotContains(t, text, "user:pw")
	assert.Contains(t, text, "status")
	assert.Contains(t, text, "[redacted]")
}

func TestDoRefusesARelativeURL(t *testing.T) {
	client := newClient(t, config.Default())
	_, err := client.Do(t.Context(), Request{URL: "/v1/items"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")
}

func TestDoKeepsAnOpenHostFromBlockingAnother(t *testing.T) {
	var downHits atomic.Int32
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downHits.Add(1)
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(down.Close)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("up"))
	}))
	t.Cleanup(up.Close)

	cfg := fastRetries(0)
	cfg.Fetcher.CircuitFailureThreshold = 2
	cfg.Fetcher.CircuitSuccessThreshold = 1
	cfg.Fetcher.CircuitResetTimeout = time.Hour
	client := newClient(t, cfg)

	_, err := client.Do(t.Context(), Request{URL: down.URL})
	require.ErrorIs(t, err, ErrStatus)
	_, err = client.Do(t.Context(), Request{URL: down.URL})
	require.ErrorIs(t, err, ErrStatus)
	_, err = client.Do(t.Context(), Request{URL: down.URL})
	require.ErrorIs(t, err, ErrCircuitOpen)
	assert.Equal(t, int32(2), downHits.Load())

	res, err := client.Do(t.Context(), Request{URL: up.URL})
	require.NoError(t, err)
	assert.Equal(t, "up", string(res.Body))
}

func TestDoInjectsTheTraceParent(t *testing.T) {
	var traceparent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceparent = r.Header.Get("traceparent")
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)

	provider := sdktrace.NewTracerProvider()
	ctx, span := provider.Tracer("test").Start(t.Context(), "parent")
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	client := newClient(t, config.Default())
	_, err := client.Do(ctx, Request{URL: server.URL + "/items"})
	require.NoError(t, err)
	span.End()

	require.NotEmpty(t, traceparent)
	assert.Contains(t, traceparent, span.SpanContext().TraceID().String())
}

func TestDoRefusesABodyOverTheLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("0123456789abcdef"))
	}))
	t.Cleanup(server.Close)

	cfg := fastRetries(0)
	cfg.Fetcher.MaxBodyBytes = 8
	client := newClient(t, cfg)
	_, err := client.Do(t.Context(), Request{URL: server.URL})
	require.ErrorIs(t, err, ErrNetwork)
	assert.NotContains(t, err.Error(), "0123456789")
}

func TestRedactStripsCredentials(t *testing.T) {
	line := redact("authorization: Bearer abc token=abc https://user:secret@host/a?token=abc")
	assert.NotContains(t, line, "abc")
	assert.NotContains(t, line, "secret")
	assert.Contains(t, line, "[redacted]")
	assert.Contains(t, line, "https://host/a")
}

func TestValidationRejectsARetryStorm(t *testing.T) {
	cfg := config.Default()
	cfg.Database.URL = "postgres://saka@localhost:5432/saka?sslmode=disable"
	cfg.Auth.SecretKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cfg.Fetcher.RetryCount = 100
	err := cfg.Validate()
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "fetcher.retry_count")
}

func newClient(t *testing.T, cfg config.Config) *Client {
	t.Helper()
	return newClientWithLog(t, cfg, slog.New(slog.DiscardHandler))
}

func newClientWithLog(t *testing.T, cfg config.Config, log *slog.Logger) *Client {
	t.Helper()
	client, err := New(cfg, log)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Shutdown(context.Background()) })
	return client
}

// fastRetries is the default configuration with the backoff crushed so a
// retry test does not wait the deployment's second-scale floor. The breaker
// stays above the retry count, which is the same rule Validate enforces.
func fastRetries(count int) config.Config {
	cfg := config.Default()
	cfg.Fetcher.RetryCount = count
	cfg.Fetcher.RetryWait = time.Millisecond
	cfg.Fetcher.RetryMaxWait = 2 * time.Millisecond
	cfg.Fetcher.CircuitFailureThreshold = count + 5
	return cfg
}

type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *logCapture) Handle(_ context.Context, record slog.Record) error {
	var b strings.Builder
	b.WriteString(record.Message)
	record.Attrs(func(attr slog.Attr) bool {
		b.WriteByte(' ')
		b.WriteString(attr.Key)
		b.WriteByte('=')
		b.WriteString(attr.Value.String())
		return true
	})
	c.mu.Lock()
	c.lines = append(c.lines, b.String())
	c.mu.Unlock()
	return nil
}

func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }

func (c *logCapture) WithGroup(string) slog.Handler { return c }

func (c *logCapture) joined() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}
