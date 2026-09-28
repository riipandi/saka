package middleware

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// The headers a limited client reads back, and the ones the responder's
// envelope metadata copies into every response that follows.
const (
	RateLimitLimitHeader     = "X-RateLimit-Limit"
	RateLimitRemainingHeader = "X-RateLimit-Remaining"
	RateLimitResetHeader     = "X-RateLimit-Reset"
	RateLimitRetryHeader     = "Retry-After"
)

// Limiter answers one rate limit check for one key. A key is opaque here; the
// drivers give it its backend and its window. The policy rides with the call,
// because the budget a key counts against is the bucket's, and the bucket is
// decided by the classifier at the mount site.
type Limiter interface {
	Allow(ctx context.Context, key string, policy Policy) (Result, error)
}

// Policy is one bucket's budget: how many requests a key may spend per window.
type Policy struct {
	Limit  int
	Window time.Duration
}

// RateClass is a named policy. The name is what the limiter keys the bucket
// with and what the rejection metrics carry, so a dashboard can tell the
// credential bucket from the default one.
type RateClass struct {
	Name   string
	Policy Policy
}

// Classifier answers the bucket a request path is counted under, or false
// when the limiter never counts it. The transport receives one from the
// composition root, built over the guard's policy tables; the tables are what
// make the classification a decision, not a default.
type Classifier func(path string) (RateClass, bool)

// Result is the outcome of one check. A limited result carries a RetryAfter
// the middleware turns into the header the client asked for.
type Result struct {
	Limited    bool
	Limit      int
	Remaining  int
	ResetAt    time.Time
	RetryAfter time.Duration
}

// Refuse answers one limited request. Each surface owns the wire form: the
// REST surface refuses with the responder envelope, the Connect surface with
// the connect error its client parses. The X-RateLimit-* headers and
// Retry-After are already on the response when Refuse runs.
type Refuse func(w http.ResponseWriter, r *http.Request)

// RateLimit throttles the requests a client may make, keyed by its address
// within the bucket the classifier names the path with.
//
// surface names the route family the middleware throttles — "rest" or "rpc" —
// and is the label the rejection counter carries, so a dashboard can tell
// which protocol is being limited without inferring it from the path.
//
// A path the classifier does not name is not counted: it passes before the
// check runs, and its answer carries no rate-limit headers, because no budget
// was spent answering it. The limiter's counted surface is a decision the
// guard's tables declare, not a default every request falls into.
//
// A limited request is refused by Refuse — in the protocol of the surface it
// reached — with a Retry-After header before any route runs; every counted
// response carries the X-RateLimit-* headers, so a well-behaved client can
// pace itself and the envelope metadata the responder publishes stays filled.
//
// The excluded prefixes are the paths the limiter never sees at all — the
// health endpoints, a webhook a partner posts to. They are named by the
// caller at the mount site, in the one list the router composes its pipeline
// from. A prefix matches the paths under it, so an exclusion of
// "/api/healthz" also spares "/api/healthz/deep".
//
// A limiter that cannot answer — a database that is down — lets the request
// through. The limiter is a protection of the service, and taking the API
// down to enforce it inverts the relationship: a degraded backend costs some
// throttling, never the service itself. The driver logs its own failures, and
// the pass-through is counted, so a silent degradation still shows up as a
// rising "error" outcome on the rejection counter.
//
// The key is the bucket and the connection's host without its port. Behind a
// proxy every address is the proxy's, which makes a bucket's limit global
// rather than per client; a deployment that terminates TLS on the
// application itself gets honest keys.
func RateLimit(surface string, limiter Limiter, refuse Refuse, classify Classifier, excluded ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if limiter == nil {
			return next
		}
		metrics := rateLimitInstrumentation()
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			class, counted := classify(r.URL.Path)

			// A path the policy tables do not name, and an excluded one, are
			// answered before any check runs: an exempt route costs the
			// backend no round trip at all, not merely one it would have
			// passed.
			if !counted {
				metrics.requests.Add(r.Context(), 1, metric.WithAttributes(
					attribute.String("surface", surface),
					attribute.String("outcome", outcomeExcluded),
				))
				next.ServeHTTP(w, r)
				return
			}
			for _, prefix := range excluded {
				if strings.HasPrefix(r.URL.Path, prefix) {
					metrics.requests.Add(r.Context(), 1, metric.WithAttributes(
						attribute.String("surface", surface),
						attribute.String("outcome", outcomeExcluded),
					))
					next.ServeHTTP(w, r)
					return
				}
			}

			result, err := limiter.Allow(r.Context(), rateLimitKey(class.Name, r), class.Policy)
			if err != nil {
				metrics.requests.Add(r.Context(), 1, metric.WithAttributes(
					attribute.String("surface", surface),
					attribute.String("outcome", outcomeDegraded),
				))
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set(RateLimitLimitHeader, strconv.Itoa(result.Limit))
			w.Header().Set(RateLimitRemainingHeader, strconv.Itoa(max(result.Remaining, 0)))
			if !result.ResetAt.IsZero() {
				w.Header().Set(RateLimitResetHeader, strconv.FormatInt(result.ResetAt.Unix(), 10))
			}

			if result.Limited {
				seconds := max(int64(result.RetryAfter/time.Second), 1)
				w.Header().Set(RateLimitRetryHeader, strconv.FormatInt(seconds, 10))
				metrics.requests.Add(r.Context(), 1, metric.WithAttributes(
					attribute.String("surface", surface),
					attribute.String("outcome", outcomeLimited),
					attribute.String("bucket", class.Name),
				))
				refuse(w, r)
				return
			}

			metrics.requests.Add(r.Context(), 1, metric.WithAttributes(
				attribute.String("surface", surface),
				attribute.String("outcome", outcomeAllowed),
				attribute.String("bucket", class.Name),
			))
			next.ServeHTTP(w, r)
		})
	}
}

// rateLimitKey names the bucket-and-address pair one check counts. The form
// has to satisfy the rate_limits key check — lowercase alphanumerics,
// underscores, colons — which is why an address loses its dots before it
// becomes a key. The bucket stands first, so one client's budgets read
// together in the table.
func rateLimitKey(bucket string, r *http.Request) string {
	return sanitizeKey(bucket) + ":ip_" + sanitizeKey(clientIP(r))
}

// sanitizeKey reduces any string to the alphabet the rate_limits check allows.
func sanitizeKey(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == ':':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, s)
}

// retryAfterPattern reads the retry hint the check function raises with its
// SQLSTATE. The detail is a rendered sentence, so the parse is best effort:
// the window is the fallback, and a mismatch costs a client one polite wait.
var retryAfterPattern = regexp.MustCompile(`Retry after:\s*(\d+)`)

func retryAfterFromDetail(detail string, window time.Duration) time.Duration {
	if match := retryAfterPattern.FindStringSubmatch(detail); match != nil {
		if seconds, err := strconv.ParseInt(match[1], 10, 64); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return window
}
