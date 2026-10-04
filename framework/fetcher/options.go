package fetcher

import "time"

// Options is what the client is built from. Every field is explicit, so a
// caller cannot construct a client whose behavior it cannot name; the values
// come from whoever resolves the configuration, not from this package reading
// anything itself.
type Options struct {
	// Scope is the instrumentation scope name the spans and the meter carry.
	// A deployment's telemetry identity is the caller's; the framework
	// defaults to its own word.
	Scope string

	// TelemetryNamespace prefixes the metric instrument names. Empty means
	// the instruments carry their bare domain names ("fetch.requests"); an
	// application that namespaces its series by product passes the prefix it
	// wants its metrics to carry.
	TelemetryNamespace string

	// UserAgent is the product token sent on every request. It names the
	// caller, not this package.
	UserAgent string
	// Timeout bounds one attempt. A caller's context deadline still ends the
	// whole call, retries included.
	Timeout time.Duration
	// RetryCount is how many extra attempts follow a transient failure.
	// Zero disables retry. POST and PATCH are not retried: a second
	// submission can apply the operation twice.
	RetryCount int
	// RetryWait is the floor of the exponential backoff, and RetryMaxWait is
	// the ceiling. The wait is jittered so retries from many processes do
	// not land together.
	RetryWait    time.Duration
	RetryMaxWait time.Duration
	// CircuitFailureThreshold is how many failed attempts open the breaker.
	// It stays above RetryCount so the retries of one call cannot open it.
	CircuitFailureThreshold int
	// CircuitSuccessThreshold is how many successful probes close an open
	// breaker. CircuitResetTimeout is how long it stays open before the
	// first probe. Each upstream host has its own breaker, so one host
	// opening does not stop calls to another.
	CircuitSuccessThreshold int
	CircuitResetTimeout     time.Duration
	// MaxBodyBytes is how much of a response body is kept. The rest is
	// refused, so an upstream cannot grow the process without a bound.
	MaxBodyBytes int64
}
