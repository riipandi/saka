// Package fetcher is the outbound HTTP client integrations call external
// services through.
//
// One client is built from the configuration and shared by the process. Resty
// issues the request; this package decides which failures are transient, which
// error class a caller can match, and which parts of a call are safe to log.
package fetcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"resty.dev/v3"

	"github.com/riipandi/tango/internal/config"
)

// Request is one outbound call. URL may be absolute, or relative to the
// configured base URL. Query and Headers are added to that request only.
// Body is encoded as JSON when it is a struct or a map; a string or a byte
// slice is sent as-is. NoRedirect turns redirect following off — a signed
// call whose signature headers must not travel to a redirect target, and a
// caller that must not be led to a host the URL did not name.
type Request struct {
	Method  string
	URL     string
	Query   url.Values
	Headers http.Header
	Body    any
	// NoRedirect answers a 3xx as the attempt's final answer instead of
	// following it. Absent means the shared policy applies.
	NoRedirect bool
}

// Response is the answer a call received. Body is present for an HTTP error
// status as well as a success, so a caller can read an upstream error
// document. It is never written to the log.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Attempts   int
}

// Client calls external HTTP services. It is safe for concurrent use.
// Each host has its own circuit breaker. The hosts share one connection
// pool. Shutdown releases the idle connections; the composition root calls it.
type Client struct {
	mu         sync.Mutex
	hosts      map[string]*resty.Client
	baseHTTP   *http.Client
	log        *slog.Logger
	metrics    *fetchMetrics
	retryCount int
	maxBody    int64
	settings   config.Fetcher
}

// New builds the client the configuration describes.
//
// log is the process logger. Nil discards every line, which is what a test
// that is not reading the log wants. The client does not enable Resty's
// debug log: that log includes request and response bodies.
func New(cfg config.Config, log *slog.Logger) (*Client, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	settings := cfg.Fetcher
	if err := ready(settings); err != nil {
		return nil, err
	}

	// The dial and the handshake share the attempt budget. A transport
	// timeout longer than the attempt would still be cut by the attempt
	// context; keeping them equal means neither one waits alone.
	// One transport is shared by every host client, so connections to a
	// healthy host stay pooled while another host's breaker is open.
	seed := resty.NewWithTransportSettings(&resty.TransportSettings{
		DialerTimeout:         settings.Timeout,
		TLSHandshakeTimeout:   settings.Timeout,
		ResponseHeaderTimeout: settings.Timeout,
	})
	seed.SetCookieJar(nil)
	baseHTTP := seed.Client()
	if err := seed.Close(); err != nil {
		return nil, err
	}

	return &Client{
		hosts:      make(map[string]*resty.Client),
		baseHTTP:   baseHTTP,
		log:        log,
		metrics:    newFetchMetrics(),
		retryCount: settings.RetryCount,
		maxBody:    settings.MaxBodyBytes,
		settings:   settings,
	}, nil
}

// restyFor returns the client whose breaker belongs to host. host is
// scheme://host, with no path and no query. The client is built on first
// use and kept until Shutdown.
func (c *Client) restyFor(host string) *resty.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.hosts[host]; ok {
		return existing
	}
	created := c.newHostClient(host)
	c.hosts[host] = created
	return created
}

// newHostClient builds a Resty client for one host. It shares the process
// connection pool and owns the breaker for that host.
func (c *Client) newHostClient(host string) *resty.Client {
	// Both thresholds are non-negative here: ready refused anything else.
	// The comparison is what makes the conversion to the breaker's uint64
	// a value that cannot wrap.
	failures := c.settings.CircuitFailureThreshold
	successes := c.settings.CircuitSuccessThreshold
	if failures < 0 {
		failures = 0
	}
	if successes < 0 {
		successes = 0
	}
	breaker := resty.NewCircuitBreakerCount(
		uint64(failures),
		uint64(successes),
		c.settings.CircuitResetTimeout,
		breakerFailure,
	)
	breaker.OnStateChange(func(from, to resty.CircuitBreakerState) {
		c.log.Warn("fetcher: circuit breaker",
			"target", host, "from", circuitState(from), "to", circuitState(to))
		if to == resty.CircuitBreakerStateOpen {
			c.metrics.recordBreakerOpen(context.Background(), host)
		}
	})
	breaker.OnTrigger(func(*resty.Request, error) {
		// The request is not logged: it can carry a credential. The host
		// is the only fact that matters, and it is the breaker's key.
		c.log.Warn("fetcher: circuit open", "target", host)
	})

	httpClient := resty.NewWithClient(c.baseHTTP)
	httpClient.
		SetLogger(slogAdapter{log: c.log}).
		SetDebug(false).
		SetTimeout(c.settings.Timeout).
		SetHeader(headerUserAgent, c.settings.UserAgent).
		SetRetryCount(c.settings.RetryCount).
		SetRetryWaitTime(c.settings.RetryWait).
		SetRetryMaxWaitTime(c.settings.RetryMaxWait).
		AddRetryConditions(retryable).
		SetCircuitBreaker(breaker).
		SetResponseBodyLimit(c.maxBody).
		// A redirect to another host must not carry Authorization or Cookie,
		// and a request that marked itself no-redirect stops at its 3xx.
		SetRedirectPolicy(redirectGate(), resty.RedirectHeaderStripSensitivePolicy(true))
	return httpClient
}

// ctxKeyNoRedirect marks a request whose 3xx answers are final.
type ctxKeyNoRedirect struct{}

// redirectGate is the fetcher's own redirect policy: a request that carries
// the no-redirect marker answers its 3xx as the response instead of
// following it. The signature headers a signed call carries would otherwise
// travel to whatever host the redirect names.
func redirectGate() resty.RedirectPolicy {
	return resty.RedirectPolicyFunc(func(req *http.Request, via []*http.Request) error {
		if req.Context().Value(ctxKeyNoRedirect{}) == true {
			return http.ErrUseLastResponse
		}
		return nil
	})
}

// headerUserAgent is the canonical header name, so a caller-supplied
// User-Agent replaces the product token instead of being sent beside it.
const headerUserAgent = "User-Agent"

// ready refuses a configuration that would wait without a deadline or retry
// without a bound. Validate is the message a file sees; this is the guard
// on the constructor a test or a caller can hit without it.
func ready(settings config.Fetcher) error {
	switch {
	case settings.Timeout <= 0:
		return fmt.Errorf("fetcher: timeout must be positive")
	case settings.RetryCount < 0:
		return fmt.Errorf("fetcher: retry count must not be negative")
	case settings.RetryWait <= 0 || settings.RetryMaxWait < settings.RetryWait:
		return fmt.Errorf("fetcher: retry wait must be positive and within its maximum")
	case settings.CircuitFailureThreshold <= settings.RetryCount:
		return fmt.Errorf("fetcher: circuit failure threshold must exceed the retry count")
	case settings.CircuitSuccessThreshold <= 0:
		return fmt.Errorf("fetcher: circuit success threshold must be positive")
	case settings.CircuitResetTimeout < settings.Timeout:
		return fmt.Errorf("fetcher: circuit reset timeout must not be shorter than the attempt timeout")
	case settings.UserAgent == "":
		return fmt.Errorf("fetcher: user agent must not be empty")
	case settings.MaxBodyBytes <= 0:
		return fmt.Errorf("fetcher: max body must be positive")
	default:
		return nil
	}
}

// Do sends req and returns the response it received.
//
// An HTTP status of 400 or above is an error and the response is still
// returned, so the caller can read the upstream document. A transport
// failure, a timeout, a cancellation, an open circuit, and a string of
// retries that never succeeded are different errors: errors.Is matches the
// sentinel for each. The caller's context ends the attempt and any wait
// between retries.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	method = strings.ToUpper(method)

	absolute, host, err := resolve(req.URL)
	if err != nil {
		c.metrics.recordRequest(ctx, "", outcomeInvalid, 0, 0)
		return nil, err
	}
	target := safeTarget("", absolute)
	ctx, span := startSpan(ctx, method, target)
	var (
		out        *Response
		classified error
	)
	defer func() {
		status := 0
		if out != nil {
			status = out.StatusCode
		}
		finishSpan(span, status, classified)
	}()

	headers := req.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	injectTrace(ctx, headers)

	call := c.restyFor(host).R().SetContext(ctx).SetHeaderMultiValues(headers)
	if req.NoRedirect {
		// The gate rides the request's context: the redirect policy lives on
		// the shared host client, so the request itself carries the choice.
		call.SetContext(context.WithValue(ctx, ctxKeyNoRedirect{}, true))
	}
	if len(req.Query) > 0 {
		call.SetQueryParamsFromValues(req.Query)
	}
	if req.Body != nil {
		call.SetBody(req.Body)
	}

	started := time.Now()
	res, callErr := call.Execute(method, absolute)
	var readErr error
	out, readErr = responseFrom(res, c.maxBody)
	if readErr != nil && callErr == nil {
		callErr = readErr
	}
	classified = classify(c.retryCount, "", method, absolute, out.Attempts, res, callErr)
	attempts := 0
	if out != nil {
		attempts = out.Attempts
	}
	c.metrics.recordRequest(ctx, host, c.metrics.outcomeFor(classified), time.Since(started), attempts)
	c.logCall(ctx, method, target, out, classified, time.Since(started))
	if classified != nil {
		return out, classified
	}
	return out, nil
}

// resolve returns the absolute URL and the host key the breaker is stored
// under. The URL is the caller's: an upstream address is hardcoded at the
// call site, so a relative URL has nothing to join to.
func resolve(rawURL string) (absolute, host string, err error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("fetcher: url: %w", err)
	}
	if parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "", fmt.Errorf("fetcher: url must be an absolute http or https URL")
	}
	// Userinfo stays on the request URL: it is a credential the caller set.
	// The host key and the logs use the host alone.
	return parsed.String(), parsed.Scheme + "://" + parsed.Host, nil
}

// Shutdown closes every host client and the shared idle connections.
// In-flight calls finish on their own context; this does not cancel them.
func (c *Client) Shutdown(context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var errs []error
	for _, httpClient := range c.hosts {
		errs = append(errs, httpClient.Close())
	}
	clear(c.hosts)
	if c.baseHTTP != nil {
		c.baseHTTP.CloseIdleConnections()
	}
	return errors.Join(errs...)
}

// responseFrom copies the fields a caller is allowed to keep. The Resty
// response stays inside this package: its request still holds headers.
func responseFrom(res *resty.Response, maxBody int64) (*Response, error) {
	out := &Response{Attempts: attemptsOf(res)}
	if res == nil {
		return out, nil
	}
	out.StatusCode = statusCode(res)
	if res.RawResponse != nil {
		out.Header = res.Header().Clone()
	}
	body, err := readBody(res, maxBody)
	out.Body = body
	return out, err
}

// readBody returns the buffered body, or reads it once, and never more than
// maxBody. A body Resty already refused for size stays refused.
func readBody(res *resty.Response, maxBody int64) ([]byte, error) {
	if maxBody < 1 {
		maxBody = 1
	}
	if buffered := res.Bytes(); len(buffered) > 0 {
		if int64(len(buffered)) > maxBody {
			return nil, errResponseTooLarge
		}
		return buffered, nil
	}
	if res.Body == nil {
		return nil, nil
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return body, err
	}
	if int64(len(body)) > maxBody {
		return nil, errResponseTooLarge
	}
	return body, nil
}

func attemptsOf(res *resty.Response) int {
	if res == nil || res.Request == nil || res.Request.Attempt < 1 {
		return 1
	}
	return res.Request.Attempt
}

func statusCode(res *resty.Response) int {
	if res == nil || res.RawResponse == nil {
		return 0
	}
	return res.StatusCode()
}

// logCall records the outcome of a call without the URL query, the body, or
// any header. A success is a debug line; an HTTP client error is a warning;
// everything else is an error.
func (c *Client) logCall(ctx context.Context, method, target string, res *Response, err error, elapsed time.Duration) {
	status := 0
	attempts := 1
	if res != nil {
		status = res.StatusCode
		attempts = res.Attempts
	}
	attrs := []any{
		"method", method,
		"target", target,
		"status", status,
		"attempts", attempts,
		"duration", elapsed.String(),
	}
	switch {
	case err == nil:
		c.log.DebugContext(ctx, "fetcher: call", attrs...)
	case isStatus(err) && status >= 400 && status < 500:
		c.log.WarnContext(ctx, "fetcher: call", append(attrs, "err", err.Error())...)
	default:
		c.log.ErrorContext(ctx, "fetcher: call", append(attrs, "err", err.Error())...)
	}
}
