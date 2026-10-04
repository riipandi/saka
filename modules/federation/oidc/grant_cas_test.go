package oidc

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
)

// The grant compare-and-swap: a request that read a grant may only
// rewrite the row while it still holds the document that request read.
// Two requests redeeming the same one-time code both read the
// unconsumed document, and exactly one save finds it — the other is
// refused before any token could be issued.

// seedDeviceGrant plants one live device grant under a real client row —
// the pointer row's foreign key demands one — and answers the device
// code it stored.
func seedDeviceGrant(t *testing.T, pool *datastore.Postgres, deviceCode string) {
	t.Helper()
	owner := seedAccount(t, pool, "langdon")
	service := testService(t, pool)
	issued, err := service.Create(t.Context(), owner, createParams("Device Portal"))
	require.NoError(t, err)

	store := grantStore{protocolStore: protocolStore{pool: pool}}
	require.NoError(t, store.SaveGrant(t.Context(), &goidc.Grant{
		ID:                  "grant-" + deviceCode,
		ClientID:            issued.Client.ID,
		Subject:             "11111111-1111-5111-8111-111111111111",
		Username:            "langdon",
		Scopes:              "openid",
		CreatedAt:           int(time.Now().Unix()),
		DeviceCode:          deviceCode,
		DeviceCodeExpiresAt: int(time.Now().Add(time.Minute).Unix()),
	}))
}

// errAlreadyRedeemed stands in for the invalid-grant refusal the library
// itself answers before any save: a request that loads an already
// consumed device code never reaches its save.
var errAlreadyRedeemed = errors.New("test: the device code has already been redeemed")

// redeem loads the grant through the device-code pointer and saves it
// back with the consumption stamped — the exact sequence the library's
// token handler runs, its consumed check included, minus the issuance
// itself.
func redeem(t *testing.T, pool *datastore.Postgres, deviceCode string) error {
	t.Helper()
	ctx := withGrantSnapshotCache(t.Context())
	store := grantStore{protocolStore: protocolStore{pool: pool}}
	grant, err := store.GrantByDeviceCode(ctx, deviceCode)
	if err != nil {
		return err
	}
	if grant.DeviceCodeConsumedAt != 0 {
		return errAlreadyRedeemed
	}
	grant.DeviceCodeConsumedAt = int(time.Now().Unix())
	return store.SaveGrant(ctx, grant)
}

func TestADeviceCodeRedeemsExactlyOnce(t *testing.T) {
	pool := migratedPool(t)
	seedDeviceGrant(t, pool, "device-plain-1")

	const contenders = 8
	var successes atomic.Int64
	var lost atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := redeem(t, pool, "device-plain-1")
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrGrantConcurrentlyModified),
				errors.Is(err, goidc.ErrNotFound),
				errors.Is(err, errAlreadyRedeemed):
				// The loser's three shapes: the compare-and-swap caught
				// its stale save, the winner's save deleted the pointer
				// first (a not-found the library answers as an invalid
				// grant), or the document it read was already consumed.
				lost.Add(1)
			default:
				t.Errorf("unexpected redemption error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int64(1), successes.Load(), "exactly one request may redeem the code")
	assert.Equal(t, int64(contenders-1), lost.Load(), "every loser is refused, one way or the other")

	// The grant is consumed and its pointer is gone: a later redemption
	// is a not-found, the replay shape the protocol answers.
	store := grantStore{protocolStore: protocolStore{pool: pool}}
	grant, err := store.Grant(t.Context(), "grant-device-plain-1")
	require.NoError(t, err)
	assert.NotZero(t, grant.DeviceCodeConsumedAt)
	_, err = store.GrantByDeviceCode(t.Context(), "device-plain-1")
	assert.ErrorIs(t, err, goidc.ErrNotFound)
}

func TestTheRedeemingRequestCanStillRevokeOnAFailedIssuance(t *testing.T) {
	pool := migratedPool(t)
	seedDeviceGrant(t, pool, "device-plain-2")

	ctx := withGrantSnapshotCache(t.Context())
	store := grantStore{protocolStore: protocolStore{pool: pool}}
	grant, err := store.GrantByDeviceCode(ctx, "device-plain-2")
	require.NoError(t, err)

	grant.DeviceCodeConsumedAt = int(time.Now().Unix())
	require.NoError(t, store.SaveGrant(ctx, grant))

	// The library's failure path: token issuance broke, the same request
	// revokes the grant it just consumed. Its own last document is the
	// snapshot the save demands, so the revocation lands.
	grant.RevokedAt = int(time.Now().Unix())
	require.NoError(t, store.SaveGrant(ctx, grant), "the consuming request's revocation must not be refused by its own consumption")

	stored, err := store.Grant(t.Context(), "grant-device-plain-2")
	require.NoError(t, err)
	assert.NotZero(t, stored.RevokedAt)
}

func TestAStaleGrantSaveIsRefused(t *testing.T) {
	pool := migratedPool(t)
	seedDeviceGrant(t, pool, "device-plain-3")

	// One request reads the grant; another request — no snapshot armed,
	// the plain write the creation and approval paths use — rewrites the
	// row underneath it. The first request's save must now refuse.
	ctx := withGrantSnapshotCache(t.Context())
	store := grantStore{protocolStore: protocolStore{pool: pool}}
	stale, err := store.GrantByDeviceCode(ctx, "device-plain-3")
	require.NoError(t, err)
	stale.Username = "langdon"

	plain := grantStore{protocolStore: protocolStore{pool: pool}}
	fresh, err := plain.Grant(t.Context(), "grant-device-plain-3")
	require.NoError(t, err)
	fresh.Username = "vetra"
	require.NoError(t, plain.SaveGrant(t.Context(), fresh))

	// No re-read between the foreign write and this save: a fresh read
	// would re-arm the snapshot, and the stale save is what must fail.
	err = store.SaveGrant(ctx, stale)
	require.ErrorIs(t, err, ErrGrantConcurrentlyModified)
}

func TestAGrantCreatedOnTheRouteSavesWithoutASnapshot(t *testing.T) {
	pool := migratedPool(t)

	// The device approval creates the grant on a route whose request
	// carries an armed cache but has read nothing: the plain write holds.
	owner := seedAccount(t, pool, "langdon")
	service := testService(t, pool)
	issued, err := service.Create(t.Context(), owner, createParams("Fresh Portal"))
	require.NoError(t, err)

	ctx := withGrantSnapshotCache(t.Context())
	store := grantStore{protocolStore: protocolStore{pool: pool}}
	require.NoError(t, store.SaveGrant(ctx, &goidc.Grant{
		ID:                  "grant-fresh",
		ClientID:            issued.Client.ID,
		Subject:             "11111111-1111-5111-8111-111111111111",
		Username:            "langdon",
		Scopes:              "openid",
		CreatedAt:           int(time.Now().Unix()),
		DeviceCode:          "device-plain-4",
		DeviceCodeExpiresAt: int(time.Now().Add(time.Minute).Unix()),
	}))

	grant, err := store.Grant(t.Context(), "grant-fresh")
	require.NoError(t, err)
	assert.Equal(t, "grant-fresh", grant.ID)
}
