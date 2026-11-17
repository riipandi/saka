package user

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	conttest "github.com/riipandi/saka/pkg/testutils"
)

// A profile edit reads the row under its lock and writes the profile columns
// only, so a change that commits while the edit is in flight is read by the
// edit, never overwritten by it.

// editWhileAnotherTransactionHoldsTheRow starts a self-service profile edit
// behind a transaction that has locked the account and applied change, lets
// the edit block on the lock, commits the change, and waits for the edit.
func editWhileAnotherTransactionHoldsTheRow(t *testing.T, change string) (pool *datastore.Postgres, id string) {
	t.Helper()
	conttest.SkipWithoutDocker(t)

	pool = migratedPool(t)
	service := testService(t, pool)
	created, err := service.CreateUser(t.Context(), CreateParams{
		Username: "winner", Email: "winner@example.com", FirstName: "Win", LastName: "Ner", EmailVerified: true,
	})
	require.NoError(t, err)

	holding := make(chan struct{})
	release := make(chan struct{})
	committed := make(chan error, 1)
	go func() {
		committed <- pool.WithTx(t.Context(), func(ctx context.Context, tx datastore.Querier) error {
			if err := service.repo.LockUser(ctx, tx, IDToUUID(mustID(t, created.ID))); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, change, IDToUUID(mustID(t, created.ID))); err != nil {
				return err
			}
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding

	edited := make(chan error, 1)
	go func() {
		_, editErr := service.UpdateCurrentUser(t.Context(), created.ID, ProfileParams{
			FirstName: "Edited", LastName: "Name", DisplayName: "Edited Name",
		})
		edited <- editErr
	}()

	select {
	case <-edited:
		t.Fatal("the profile edit did not wait for the row lock")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-committed)
	require.NoError(t, <-edited)
	return pool, created.ID
}

func TestAProfileEditCannotUndoAConcurrentDisable(t *testing.T) {
	pool, id := editWhileAnotherTransactionHoldsTheRow(t, `UPDATE public.users SET disabled = true WHERE id = $1`)

	var disabled bool
	var display string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT disabled, display_name FROM public.users WHERE id = (SELECT $1::uuid)`, IDToUUID(mustID(t, id))).
		Scan(&disabled, &display))
	assert.True(t, disabled, "the administrator's disable survives the edit")
	assert.Equal(t, "Edited Name", display, "and the edit still lands")
}

func TestAProfileEditCannotRevertAnEmailChange(t *testing.T) {
	pool, id := editWhileAnotherTransactionHoldsTheRow(t,
		`UPDATE public.users SET email = 'changed@example.com' WHERE id = $1`)

	var email string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT email FROM public.users WHERE id = $1`, IDToUUID(mustID(t, id))).Scan(&email))
	assert.Equal(t, "changed@example.com", email)
}

func TestAnAddressDiffersOnlyByCaseIsTheSameAccount(t *testing.T) {
	conttest.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	_, err := service.CreateUser(t.Context(), CreateParams{Username: "first", Email: "Alice@Example.com", FirstName: "Al", LastName: "Ice"})
	require.NoError(t, err)

	_, err = service.CreateUser(t.Context(), CreateParams{Username: "second", Email: "alice@example.com", FirstName: "Al", LastName: "Ice"})
	require.ErrorIs(t, err, ErrAccountExists, "the address is one account whatever its case")

	var stored string
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT email FROM public.users WHERE email = 'ALICE@EXAMPLE.COM'`).Scan(&stored))
	assert.Equal(t, "Alice@Example.com", stored, "a lookup folds case and the row keeps what the holder typed")
}
