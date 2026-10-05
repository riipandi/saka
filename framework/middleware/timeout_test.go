package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimeoutBoundsTheHandlerContext(t *testing.T) {
	handler := Timeout(50 * time.Millisecond)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))

	start := time.Now()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	// The handler returned when the deadline fired, not when it was written:
	// a slow handler honours the context the middleware handed it.
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestTimeoutSetsTheDeadlineItWasGiven(t *testing.T) {
	var deadline time.Time
	handler := Timeout(time.Minute)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			deadline, _ = r.Context().Deadline()
		}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	require.False(t, deadline.IsZero())
	assert.InDelta(t, time.Minute, time.Until(deadline), float64(5*time.Second))
}

func TestTimeoutWithoutADurationLeavesTheRequestAlone(t *testing.T) {
	var deadline time.Time
	handler := Timeout(0)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			deadline, _ = r.Context().Deadline()
		}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	assert.True(t, deadline.IsZero(), "an unbounded request names no deadline")
}

func TestTimeoutReleasesTheTimerWhenTheHandlerIsFast(t *testing.T) {
	// A leaked timer keeps the test binary alive past its run, so the cancel
	// the middleware deferred must have fired by the time ServeHTTP returns.
	done := make(chan struct{})
	handler := Timeout(time.Hour)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			go func() {
				<-r.Context().Done()
				close(done)
			}()
		}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	select {
	case <-done:
	case <-time.After(time.Second):
		require.Fail(t, "the request context was never cancelled")
	}
}
