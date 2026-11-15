package session

import (
	"testing"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/entity"
	conttest "github.com/riipandi/saka/pkg/testutils"
)

// retentionRow inserts a session row with the end the arguments shape, the
// direct-SQL form the predicate test needs: a revocation stamp may lie in the
// past (no check guards it), while the expiry must stay in the future at
// insert — the table's own check — so the expired-never-stamped arm is judged
// through a cutoff that reaches it rather than through a past expiry.
func retentionRow(t *testing.T, pool *datastore.Postgres, userID uuid.UUID, hash string, expiresIn, revokedAgo time.Duration) {
	t.Helper()

	var revokedAt any
	if revokedAgo > 0 {
		revokedAt = time.Now().Add(-revokedAgo)
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableSessions)
	ib.Cols("user_id", "provider", "token_hash", "expires_at", "revoked_at")
	ib.Values(userID, "credential", hash, time.Now().Add(expiresIn), revokedAt)

	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
}

// countHash answers whether one hash still has a row, the assertion form the
// predicate test reads.
func countHash(t *testing.T, pool *datastore.Postgres, hash string) int {
	t.Helper()

	var count int
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(entity.TableSessions)
	sb.Where(sb.Equal("token_hash", hash))
	query, args := sb.Build()
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count
}

// TestDeleteRetentionExpiredJudgesEachRowByItsOwnEnd is the predicate's whole
// contract: a row ages from the end it actually got. A revocation older than
// the cutoff goes, a fresh one stays, a live row is never the delete's
// business, and a second call with the cutoff advanced past a nearly-closed
// expiry takes the expired-never-stamped row the first call had to leave —
// the arm a wall-clock sweep meets only after the row has aged a real month.
func TestDeleteRetentionExpiredJudgesEachRowByItsOwnEnd(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	userID := seedAccount(t, pool, "norbert")

	retentionRow(t, pool, userID, "h-live", 24*time.Hour, 0)
	retentionRow(t, pool, userID, "h-expired-unstamped", time.Minute, 0)
	retentionRow(t, pool, userID, "h-revoked-recent", 400*24*time.Hour, time.Hour)
	retentionRow(t, pool, userID, "h-revoked-old", 400*24*time.Hour, 60*24*time.Hour)

	repo := NewRepository()

	deleted, err := repo.DeleteRetentionExpired(t.Context(), pool, time.Now().AddDate(0, 0, -30))
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted, "only the revocation past the window goes")
	assert.Equal(t, 0, countHash(t, pool, "h-revoked-old"), "the old revocation is gone")
	assert.Equal(t, 1, countHash(t, pool, "h-live"))
	assert.Equal(t, 1, countHash(t, pool, "h-expired-unstamped"), "an expiry inside the window is not the sweep's business yet")
	assert.Equal(t, 1, countHash(t, pool, "h-revoked-recent"))

	// The cutoff advanced past the short expiry: the unstamped arm now
	// fires on the row the first call had to leave, and the fresh
	// revocation goes with it.
	deleted, err = repo.DeleteRetentionExpired(t.Context(), pool, time.Now().Add(2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)
	assert.Equal(t, 1, countHash(t, pool, "h-live"), "the live row survives both calls")
	assert.Zero(t, countHash(t, pool, "h-expired-unstamped"))
	assert.Zero(t, countHash(t, pool, "h-revoked-recent"))
}
