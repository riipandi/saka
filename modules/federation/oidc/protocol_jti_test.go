package oidc

import (
	"errors"
	"sync"
	"testing"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The JTI consumer: a jti claims once, a replay inside the window is
// refused with the error the library's call sites reject — anything but
// ErrNotFound — and the claims are independent of each other.

func TestJTIConsumesOnceAndRefusesTheReplay(t *testing.T) {
	pool := migratedPool(t)
	store := protocolStore{pool: pool}

	require.NoError(t, store.consumeJTI(t.Context(), "langdon"),
		"the first presentation claims the jti")
	assert.False(t, errors.Is(store.consumeJTI(t.Context(), "langdon"), goidc.ErrNotFound),
		"the library's call sites refuse every non-ErrNotFound answer, so the replay must not look like one")
	err := store.consumeJTI(t.Context(), "langdon")
	require.Error(t, err, "the second presentation inside the window is a replay")
	assert.True(t, errors.Is(err, ErrJTIReplayed))

	// A different jti never meets another's claim.
	require.NoError(t, store.consumeJTI(t.Context(), "neveu"))
	require.NoError(t, store.consumeJTI(t.Context(), "vetra"))

	// The claim survives as long as its row stands — a replay that
	// arrives before the sweep is still a replay.
	require.ErrorIs(t, store.consumeJTI(t.Context(), "langdon"), ErrJTIReplayed)
}

func TestJTIConcurrentClaimsElectOneWinner(t *testing.T) {
	pool := migratedPool(t)
	store := protocolStore{pool: pool}

	// Two concurrent presentations race over the unique index; the
	// outcome is one claim and one refusal, whichever arrives first.
	const racers = 4
	wins := make(chan struct{}, racers)
	var wg sync.WaitGroup
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if store.consumeJTI(t.Context(), "gryffindor") == nil {
				wins <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(wins)

	elected := 0
	for range wins {
		elected++
	}
	assert.Equal(t, 1, elected,
		"exactly one concurrent presentation wins the claim")
}
