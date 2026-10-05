package fetcher

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"resty.dev/v3"
)

// The classes a caller matches with errors.Is. A call that failed for more
// than one reason carries each of them: a timeout that used every retry
// matches both ErrTimeout and ErrRetryExhausted.
const (
	// ErrNetwork is a failure before an HTTP status arrived: DNS, a refused
	// connection, a reset, or a body that could not be read.
	ErrNetwork = sentinel("fetcher: network")
	// ErrTimeout is an attempt, or the caller's deadline, that ran out of time.
	ErrTimeout = sentinel("fetcher: timeout")
	// ErrCanceled is the caller's context ending for a reason other than its deadline.
	ErrCanceled = sentinel("fetcher: canceled")
	// ErrRetryExhausted means every configured retry ran and the call still failed.
	ErrRetryExhausted = sentinel("fetcher: retries exhausted")
	// ErrCircuitOpen means the breaker did not let the attempt onto the network.
	ErrCircuitOpen = sentinel("fetcher: circuit open")
	// ErrStatus is an HTTP status of 400 or above. Error.Status is the code.
	ErrStatus = sentinel("fetcher: http status")
)

// errResponseTooLarge is the body cap. It is a network-class failure: the
// status may have been successful and the document was still unusable.
var errResponseTooLarge = errors.New("fetcher: response exceeds 16MiB")

// sentinel is an error class whose text is the class itself.
type sentinel string

func (s sentinel) Error() string { return string(s) }

// Error is one failed call. Method and Target locate it; Target has no
// userinfo and no query, because both are where credentials travel. Status
// is set when the upstream answered. Kind is the class. Unwrap also yields
// ErrRetryExhausted when the retries ran out, and the context sentinel when
// the class is cancellation or a timeout.
type Error struct {
	Method   string
	Target   string
	Status   int
	Attempts int
	Kind     error

	exhausted bool
}

// Error returns a description safe to log. It does not include the underlying
// transport error: that text contains the full URL, query included.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Status > 0 {
		return fmt.Sprintf("fetcher: %s %s: status %d after %d attempts", e.Method, e.Target, e.Status, e.Attempts)
	}
	name := "failed"
	if e.Kind != nil {
		name = e.Kind.Error()
	}
	return fmt.Sprintf("fetcher: %s %s: %s after %d attempts", e.Method, e.Target, name, e.Attempts)
}

// Unwrap returns the classes this failure belongs to.
func (e *Error) Unwrap() []error {
	if e == nil || e.Kind == nil {
		return nil
	}
	out := []error{e.Kind}
	if e.exhausted {
		out = append(out, ErrRetryExhausted)
	}
	switch e.Kind {
	case ErrCanceled:
		out = append(out, context.Canceled)
	case ErrTimeout:
		out = append(out, context.DeadlineExceeded)
	}
	return out
}

// classify turns Resty's result into an Error, or nil when the call succeeded.
// res may be nil when the breaker or the transport returned before a response
// existed. attempts is the number of times the request was sent or refused.
func classify(retryCount int, baseURL, method, rawURL string, attempts int, res *resty.Response, err error) error {
	if err == nil && (res == nil || !res.IsStatusFailure()) {
		return nil
	}
	if attempts < 1 {
		attempts = 1
	}
	failure := &Error{
		Method:   method,
		Target:   safeTarget(baseURL, rawURL),
		Attempts: attempts,
		Status:   statusCode(res),
	}
	switch {
	case errors.Is(err, resty.ErrCircuitBreakerOpen):
		failure.Kind = ErrCircuitOpen
		failure.Status = 0
	case errors.Is(err, context.Canceled):
		failure.Kind = ErrCanceled
		failure.Status = 0
	case timeout(err):
		failure.Kind = ErrTimeout
		failure.Status = 0
	case errors.Is(err, errResponseTooLarge) || errors.Is(err, resty.ErrReadExceedsThresholdLimit):
		failure.Kind = ErrNetwork
	case err != nil:
		failure.Kind = ErrNetwork
		failure.Status = 0
	default:
		failure.Kind = ErrStatus
	}
	if retriesRanOut(retryCount, attempts, failure) {
		failure.exhausted = true
	}
	return failure
}

// retriesRanOut reports whether this failure consumed every configured retry.
// A cancellation and an open circuit stop the call early, so they are not
// exhaustion. A status the client does not retry is not exhaustion either.
func retriesRanOut(retryCount, attempts int, failure *Error) bool {
	if retryCount < 1 || attempts <= retryCount || failure == nil {
		return false
	}
	switch failure.Kind {
	case ErrCanceled, ErrCircuitOpen:
		return false
	case ErrStatus:
		return retryableStatus(failure.Status)
	case ErrTimeout, ErrNetwork:
		return true
	default:
		return false
	}
}

// timeout reports whether err is a deadline, including one wrapped by a URL
// error from the transport. A caller's cancellation is not a timeout.
func timeout(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

// safeTarget is the URL a log line or an error may carry: scheme, host, and
// path. The query and the userinfo are dropped because a token is commonly
// placed in either.
func safeTarget(baseURL, rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil {
		return "invalid-url"
	}
	if baseURL != "" && !parsed.IsAbs() {
		base, baseErr := url.Parse(baseURL)
		if baseErr == nil {
			parsed = base.ResolveReference(parsed)
		}
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	if parsed.Host == "" {
		path := parsed.EscapedPath()
		if path == "" {
			return "/"
		}
		return path
	}
	if parsed.Scheme == "" {
		return parsed.Host + parsed.EscapedPath()
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath()
}

// isStatus reports whether err is an HTTP status failure.
func isStatus(err error) bool {
	return errors.Is(err, ErrStatus)
}

// retryable decides whether Resty should send the request again.
//
// Most 4xx statuses are the caller's request and will fail the same way
// again. 408 and 429 ask for a later attempt. A 5xx except 501 is treated
// as transient. A network error is transient unless it is a TLS verification
// failure, a bad scheme, or too many redirects — those are properties of
// the request. The caller's context ending the call is not retried; Resty
// also stops the wait when that context ends.
func retryable(res *resty.Response, err error) bool {
	if res != nil && res.Request != nil && res.Request.Context().Err() != nil {
		return false
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false
		}
		if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
			return false
		}
		if !transientTransport(err) && !timeout(err) {
			return false
		}
		return true
	}
	if res == nil {
		return false
	}
	return retryableStatus(res.StatusCode())
}

// retryableStatus reports whether an HTTP status is worth another attempt.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	default:
		return code >= 500 && code != http.StatusNotImplemented
	}
}

// transientTransport reports whether err is a connection failure Resty did
// not already classify as permanent. The default Resty conditions only retry
// a URL error whose Temporary method is true, which a refused connection and
// a deadline no longer are.
func transientTransport(err error) bool {
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		message := urlErr.Error()
		if strings.Contains(message, "stopped after") && strings.Contains(message, "redirects") {
			return false
		}
		if strings.Contains(message, "unsupported protocol scheme") {
			return false
		}
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// circuitState is the name a log line uses. The Resty state is a number.
func circuitState(state resty.CircuitBreakerState) string {
	switch state {
	case resty.CircuitBreakerStateClosed:
		return "closed"
	case resty.CircuitBreakerStateOpen:
		return "open"
	case resty.CircuitBreakerStateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// breakerFailure counts an upstream failure toward opening the circuit.
// It matches the statuses retryableStatus retries, so a status the client
// will not retry cannot open the breaker, and a status it will retry can.
func breakerFailure(res *resty.Response) bool {
	if res == nil {
		return false
	}
	return retryableStatus(res.StatusCode())
}
