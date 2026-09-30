package jobs

import (
	"testing"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/webauthn"
	"github.com/riipandi/tango/pkg/testutils"
)

// ceremonySession writes one live ceremony row. The table's check refuses an
// already-expired insert, so aging is the sweep's clock: the rows are all
// live at insert, and the sweep judges them with a now that has moved.
func ceremonySession(t *testing.T, pool *datastore.Postgres, ttl time.Duration) {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(webauthnSessionsTable)
	sb.Cols("id", "challenge", "challenge_type", "user_verification", "credential_params", "extensions", "created_at", "expires_at")
	sb.Values(
		uuid.NewV7(),
		uuid.NewV7().String(),
		"authentication",
		"required",
		`[]`,
		`{}`,
		time.Now(),
		time.Now().Add(ttl),
	)
	query, args := sb.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
}

// countCeremonySessions answers how many ceremony rows the table holds,
// which is what the sweep is asserted on.
func countCeremonySessions(t *testing.T, pool *datastore.Postgres) int {
	t.Helper()

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM `+webauthnSessionsTable).Scan(&count))
	return count
}

// TestTheCeremonySweepReapsOnlyTheExpired pins the sweep's boundary: a
// ceremony still inside its window survives, an expired one goes, and a
// second run finds nothing left to do.
func TestTheCeremonySweepReapsOnlyTheExpired(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := testutils.MigratedPostgres(t, "webauthn_cleanup_test")
	ceremonySession(t, pool, time.Minute) // judged expired when the clock moves
	ceremonySession(t, pool, time.Hour)   // still live an hour from now
	require.Equal(t, 2, countCeremonySessions(t, pool))

	later := time.Now().Add(2 * time.Minute)
	deleted, err := deleteExpiredWebauthnSessions(t.Context(), pool, later)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	assert.Equal(t, 1, countCeremonySessions(t, pool))

	deleted, err = deleteExpiredWebauthnSessions(t.Context(), pool, later)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted, "the second sweep finds nothing left")
}

// TestTheTokenSweepReapsTheExpired pins the token half of the maintenance:
// a step-up proof minted and never spent leaves the table through the
// sweep, and a live token survives it.
func TestTheTokenSweepReapsTheExpired(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := testutils.MigratedPostgres(t, "webauthn_token_cleanup_test")
	repo := webauthn.NewRepository()
	// The table's check refuses an already-expired insert, so aging is the
	// sweep's clock: both rows go in live, and the sweep judges them with a
	// now that has moved past the short one. Each token rides its own user
	// row — the FK the tokens carry.
	now := time.Now()
	dead := tokenOwner(t, pool)
	live := tokenOwner(t, pool)
	require.NoError(t, repo.CreateReauthenticationToken(t.Context(), pool, dead, "dead-hash", now, now.Add(time.Minute)))
	require.NoError(t, repo.CreateReauthenticationToken(t.Context(), pool, live, "live-hash", now, now.Add(time.Hour)))

	swept, err := repo.DeleteExpiredTokens(t.Context(), pool, now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(1), swept)

	var count int
	require.NoError(t, pool.QueryRow(t.Context(), "SELECT count(*) FROM public.auth_tokens").Scan(&count))
	assert.Equal(t, 1, count, "the live token stays")
}

// tokenOwner seeds the user row a token's FK carries and answers its id.
func tokenOwner(t *testing.T, pool *datastore.Postgres) uuid.UUID {
	t.Helper()

	userID := uuid.NewV7()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.users (id, username, email, display_name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())`,
		userID, "hermione_"+userID.String()[:8], "hermione@example.com", "Hermione Granger")
	require.NoError(t, err)
	return userID
}
