package health

import (
	"net/http"
	"time"

	"github.com/riipandi/saka/framework/webutil"
)

// Handler returns the REST endpoint that reports health. It answers 200 when
// every required check passed and 503 otherwise, so a load balancer can act on
// the status code alone. The body follows the standard API envelope, and its
// data field carries the same result the CLI prints, so the two surfaces agree.
func Handler(checker *Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		disableCaching(w)

		result := checker.Check(r.Context())
		if result.Healthy() {
			webutil.Success(w, r, http.StatusOK, result)
			return
		}

		// The message names the failing checks, so a reader does not have to
		// open the details to learn what is wrong.
		webutil.Fail(w, r, http.StatusServiceUnavailable,
			Message(result),
			webutil.WithError(result))
	}
}

// disableCaching marks the response as uncacheable. A cached health response
// would keep reporting a stale status after the system recovered, and probes
// must observe the current state.
func disableCaching(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

// Failure describes a component that could not be reached at all, such as a
// connection pool that failed to open. It builds the result a failed check
// would have produced, so a caller prints and serializes it the same way.
func Failure(name string, err error) Result {
	now := time.Now().UTC()

	detail := CheckResult{Name: name, Status: StatusDown, Timestamp: now}
	if err != nil {
		detail.Error = err.Error()
	}
	return Result{
		Status:  GlobalUnhealthy,
		Details: map[string]CheckResult{name: detail},
	}
}
