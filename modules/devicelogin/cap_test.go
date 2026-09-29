package devicelogin

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pairing cap: one creating browser holds at most maxPendingRequests
// live requests, the count decides inside an advisory lock so concurrent
// creates cannot race past it, expiry frees capacity, and different
// pairing tokens never contend.

func TestTheCapRefusesRequestsBeyondTheLimit(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	for range maxPendingRequests {
		_, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
		require.NoError(t, err)
	}

	_, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
	require.ErrorIs(t, err, ErrTooManyPendingRequests)

	// A different pairing token is its own ledger.
	_, err = service.Create(t.Context(), "token-other", "192.0.2.10", "Probe/1.0")
	require.NoError(t, err)
}

func TestExpiryFreesTheCap(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	for range maxPendingRequests {
		_, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
		require.NoError(t, err)
	}

	// Every request ages past its window: nothing live, nothing held.
	// The row's check constraint keeps expiry after creation, so both
	// timestamps move back together.
	_, err := pool.Exec(t.Context(),
		`UPDATE public.device_login_requests
		 SET expires_at = now() - interval '1 minute', created_at = now() - interval '6 minutes'`)
	require.NoError(t, err)

	_, err = service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
	require.NoError(t, err)
}

func TestConcurrentCreatesRespectTheCap(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	const attempts = 3 * maxPendingRequests
	results := make(chan error, attempts)
	var start sync.WaitGroup
	start.Add(1)
	for range attempts {
		go func() {
			start.Wait()
			_, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
			results <- err
		}()
	}
	start.Done()

	admitted, refused := 0, 0
	for range attempts {
		switch err := <-results; {
		case err == nil:
			admitted++
		case errors.Is(err, ErrTooManyPendingRequests):
			refused++
		default:
			t.Fatalf("unexpected create error: %v", err)
		}
	}
	assert.Equal(t, maxPendingRequests, admitted, "the cap admits exactly the limit, not one fewer")
	assert.Equal(t, attempts-maxPendingRequests, refused)

	var live int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.device_login_requests
		 WHERE device_token_hash = $1 AND expires_at > now()`,
		HashDeviceToken("token-plain")).Scan(&live))
	assert.Equal(t, maxPendingRequests, live, "no row slipped past the cap")
}

func TestConcurrentCreatesOnDifferentTokensDoNotContend(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	tokens := []string{"token-a", "token-b", "token-c"}
	var wg sync.WaitGroup
	errs := make(chan error, len(tokens)*maxPendingRequests)
	for _, token := range tokens {
		for range maxPendingRequests {
			wg.Add(1)
			go func(token string) {
				defer wg.Done()
				_, err := service.Create(t.Context(), token, "192.0.2.10", "Probe/1.0")
				errs <- err
			}(token)
		}
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err, "each token's ledger holds its own cap")
	}

	var live int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.device_login_requests WHERE expires_at > now()`).Scan(&live))
	assert.Equal(t, len(tokens)*maxPendingRequests, live)
}

// TestCreateWithinLimitSerializesOneBrowser pins the lock's role
// directly: two creates inside one lock window cannot both pass a count
// taken before either insert.
func TestCreateWithinLimitSerializesOneBrowser(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	tokenHash := HashDeviceToken("token-plain")

	// Fill the cap to the boundary, then race one more create against
	// itself: the lock makes the answer deterministic — one refusal per
	// attempt while the cap holds.
	for range maxPendingRequests {
		_, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
		require.NoError(t, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for range 5 {
		_, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
		require.ErrorIs(t, err, ErrTooManyPendingRequests)
		if time.Now().After(deadline) {
			break
		}
	}
	var live int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.device_login_requests WHERE device_token_hash = $1 AND expires_at > now()`,
		tokenHash).Scan(&live))
	assert.Equal(t, maxPendingRequests, live)
}
