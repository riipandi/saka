package notification

import (
	"context"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"uuid"

	notificationv1 "github.com/riipandi/saka/codegen/proto/go/saka/notification/v1"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
)

func callerFor(t *testing.T, id uuid.UUID, expiresAt time.Time) *jwtutils.Caller {
	t.Helper()
	wire, err := user.IDFromUUIDString(id.String())
	require.NoError(t, err)
	return &jwtutils.Caller{UserID: wire.String(), ExpiresAt: expiresAt}
}

func TestAStreamEndsAtTheAccessTokensExpiry(t *testing.T) {
	handler := &rpcHandler{service: &Service{broker: NewBroker()}}
	caller := callerFor(t, uuid.NewV7(), time.Now().Add(150*time.Millisecond))

	var mu sync.Mutex
	pings := 0
	started := time.Now()
	err := handler.watch(t.Context(), caller, func(event *notificationv1.WatchEvent) error {
		mu.Lock()
		defer mu.Unlock()
		if event.Kind == "ping" {
			pings++
		}
		return nil
	})

	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "the client reconnects with a refreshed token")
	assert.Less(t, time.Since(started), 3*time.Second, "the stream ended at the expiry, not at the context's end")
	assert.Equal(t, 1, pings, "the opening ping went out first")
}

func TestACredentialWithNoExpiryKeepsItsStreamUntilTheContextEnds(t *testing.T) {
	handler := &rpcHandler{service: &Service{broker: NewBroker()}}
	caller := callerFor(t, uuid.NewV7(), time.Time{})

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	err := handler.watch(ctx, caller, func(*notificationv1.WatchEvent) error { return nil })
	assert.NoError(t, err)
}

func TestTheSixthStreamOfOneAccountIsRefused(t *testing.T) {
	handler := &rpcHandler{service: &Service{broker: NewBroker()}}
	id := uuid.NewV7()
	caller := callerFor(t, id, time.Time{})

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	opened := make(chan struct{}, MaxStreamsPerAccount)
	for range MaxStreamsPerAccount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = handler.watch(ctx, caller, func(*notificationv1.WatchEvent) error {
				select {
				case opened <- struct{}{}:
				default:
				}
				return nil
			})
		}()
	}
	for range MaxStreamsPerAccount {
		select {
		case <-opened:
		case <-time.After(3 * time.Second):
			t.Fatal("a stream under the bound never opened")
		}
	}

	err := handler.watch(t.Context(), caller, func(*notificationv1.WatchEvent) error { return nil })
	require.Error(t, err)
	assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

	// Another account is not affected, and closing a stream frees a slot.
	other, otherCancel, otherErr := handler.service.Subscribe(uuid.NewV7())
	require.NoError(t, otherErr)
	_ = other
	otherCancel()

	cancel()
	wg.Wait()
	_, again, againErr := handler.service.Subscribe(id)
	require.NoError(t, againErr)
	again()
}
