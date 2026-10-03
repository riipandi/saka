package webauthn

import (
	"testing"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// webauthnTestPool runs the migrations over a fresh container database. The
// signature matches the identity area's other suites.
func webauthnTestPool(t *testing.T) *datastore.Postgres {
	t.Helper()
	testutils.SkipWithoutDocker(t)
	return testutils.MigratedPostgres(t, "webauthn_test")
}

// insertUser writes the account row every credential's foreign key needs.
func insertUser(t *testing.T, db datastore.Querier, userID uuid.UUID) {
	t.Helper()
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto("public.users")
	sb.Cols("id", "username", "email", "display_name", "first_name", "last_name", "disabled", "created_at", "updated_at")
	sb.Values(userID, "langdon", "langdon@example.com", "Robert Langdon", "Robert", "Langdon", false, time.Now(), time.Now())
	query, args := sb.Build()
	_, err := db.Exec(t.Context(), query, args...)
	require.NoError(t, err)
}

// sampleCredential answers one enrolled passkey row with distinct bytes, so
// two rows in one test never collide.
func sampleCredential(userID uuid.UUID, tag byte) CredentialSchema {
	aaguid := "0ea242b4-43c4-4a1b-8b17-dd6d0b6baec6"
	return CredentialSchema{
		ID:              uuid.NewV7(),
		UserID:          userID,
		Name:            "Robert's YubiKey",
		CredentialID:    []byte{tag, 2, 3, 4, 5},
		PublicKey:       []byte{tag, 9, 8, 7, 6},
		SignCount:       7,
		AttestationType: "packed",
		Transport:       []byte(`["usb","nfc"]`),
		BackupEligible:  true,
		BackupState:     false,
		AAGUID:          &aaguid,
		CreatedAt:       time.Now(),
	}
}

// ---- enrolled credentials ----

func TestCredentialRoundTripCarriesTheParsedAttestation(t *testing.T) {
	pool := webauthnTestPool(t)
	ctx := t.Context()
	userID := uuid.NewV7()
	insertUser(t, pool, userID)

	repo := NewRepository()
	row := sampleCredential(userID, 1)
	require.NoError(t, repo.CreateCredential(ctx, pool, row))

	byID, err := repo.GetCredentialByID(ctx, pool, row.ID)
	require.NoError(t, err)
	assert.Equal(t, row.Name, byID.Name)
	assert.Equal(t, row.CredentialID, byID.CredentialID)
	assert.Equal(t, row.PublicKey, byID.PublicKey)
	assert.Equal(t, int64(7), byID.SignCount)
	assert.Equal(t, "packed", byID.AttestationType)
	assert.True(t, byID.BackupEligible)
	assert.False(t, byID.BackupState)
	require.NotNil(t, byID.AAGUID)
	assert.Equal(t, "0ea242b4-43c4-4a1b-8b17-dd6d0b6baec6", *byID.AAGUID)

	// The authenticator's raw id is the key usernameless sign-in resolves
	// the account by.
	byCredentialID, err := repo.GetCredentialByCredentialID(ctx, pool, row.CredentialID)
	require.NoError(t, err)
	assert.Equal(t, row.ID, byCredentialID.ID)
	assert.Equal(t, userID, byCredentialID.UserID)
}

func TestUnknownCredentialAnswersNotFound(t *testing.T) {
	pool := webauthnTestPool(t)
	repo := NewRepository()

	_, err := repo.GetCredentialByCredentialID(t.Context(), pool, []byte{1, 2, 3})
	assert.ErrorIs(t, err, ErrNoRows)

	_, err = repo.GetCredentialByID(t.Context(), pool, uuid.NewV7())
	assert.ErrorIs(t, err, ErrNoRows)
}

func TestCredentialRollOrdersByAgeAndCounts(t *testing.T) {
	pool := webauthnTestPool(t)
	ctx := t.Context()
	userID := uuid.NewV7()
	insertUser(t, pool, userID)

	repo := NewRepository()
	first := sampleCredential(userID, 1)
	second := sampleCredential(userID, 2)
	require.NoError(t, repo.CreateCredential(ctx, pool, first))
	require.NoError(t, repo.CreateCredential(ctx, pool, second))

	roll, err := repo.ListCredentials(ctx, pool, userID)
	require.NoError(t, err)
	require.Len(t, roll, 2)
	assert.Equal(t, first.ID, roll[0].ID)

	count, err := repo.CountCredentials(ctx, pool, userID)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Another account's roll stays out of the read.
	other := uuid.NewV7()
	otherCount, err := repo.CountCredentials(ctx, pool, other)
	require.NoError(t, err)
	assert.Zero(t, otherCount)
}

func TestAssertionBookkeepingAdvancesTheCounter(t *testing.T) {
	pool := webauthnTestPool(t)
	ctx := t.Context()
	userID := uuid.NewV7()
	insertUser(t, pool, userID)

	repo := NewRepository()
	row := sampleCredential(userID, 1)
	require.NoError(t, repo.CreateCredential(ctx, pool, row))

	usedAt := time.Now()
	require.NoError(t, repo.RecordAssertion(ctx, pool, row.ID, 8, true, usedAt))

	updated, err := repo.GetCredentialByID(ctx, pool, row.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(8), updated.SignCount)
	assert.True(t, updated.BackupState)
	require.NotNil(t, updated.LastUsedAt)
	assert.WithinDuration(t, usedAt, *updated.LastUsedAt, time.Second)
}

func TestRenameAndDeleteAnswerWhatHappened(t *testing.T) {
	pool := webauthnTestPool(t)
	ctx := t.Context()
	userID := uuid.NewV7()
	insertUser(t, pool, userID)

	repo := NewRepository()
	row := sampleCredential(userID, 1)
	require.NoError(t, repo.CreateCredential(ctx, pool, row))

	require.NoError(t, repo.RenameCredential(ctx, pool, row.ID, "Office key"))
	renamed, err := repo.GetCredentialByID(ctx, pool, row.ID)
	require.NoError(t, err)
	assert.Equal(t, "Office key", renamed.Name)

	deleted, err := repo.DeleteCredential(ctx, pool, row.ID)
	require.NoError(t, err)
	assert.True(t, deleted)

	deleted, err = repo.DeleteCredential(ctx, pool, row.ID)
	require.NoError(t, err)
	assert.False(t, deleted)
}

// ---- ceremony sessions ----

func sampleSession(userID *uuid.UUID, challengeType string, expiresAt time.Time) SessionSchema {
	return SessionSchema{
		ID:               uuid.NewV7(),
		UserID:           userID,
		Challenge:        uuid.NewV7().String(),
		ChallengeType:    challengeType,
		UserVerification: UserVerificationRequired,
		CredentialParams: []byte(`[{"type":"public-key","alg":-7}]`),
		Extensions:       []byte(`{}`),
		CreatedAt:        time.Now(),
		ExpiresAt:        expiresAt,
	}
}

func TestCeremonySessionIsSingleUse(t *testing.T) {
	pool := webauthnTestPool(t)
	ctx := t.Context()
	userID := uuid.NewV7()
	insertUser(t, pool, userID)

	repo := NewRepository()
	row := sampleSession(&userID, ChallengeTypeRegistration, time.Now().Add(time.Minute))
	require.NoError(t, repo.CreateSession(ctx, pool, row))

	read, err := repo.GetSession(ctx, pool, row.ID)
	require.NoError(t, err)
	assert.Equal(t, row.Challenge, read.Challenge)
	require.NotNil(t, read.UserID)
	assert.Equal(t, userID, *read.UserID)

	live, err := repo.ConsumeSession(ctx, pool, row.ID, time.Now())
	require.NoError(t, err)
	assert.True(t, live)

	// A replayed handle names no row.
	live, err = repo.ConsumeSession(ctx, pool, row.ID, time.Now())
	require.NoError(t, err)
	assert.False(t, live)

	_, err = repo.GetSession(ctx, pool, row.ID)
	assert.ErrorIs(t, err, ErrNoRows)
}

func TestExpiredCeremonySessionIsSpentByConsumption(t *testing.T) {
	pool := webauthnTestPool(t)
	ctx := t.Context()

	repo := NewRepository()
	// The table's check constraint refuses an already-expired insert, so the
	// test ages the clock instead: a row live at insert, judged a minute
	// later.
	later := time.Now().Add(time.Minute)
	row := sampleSession(nil, ChallengeTypeAuthentication, later)
	require.NoError(t, repo.CreateSession(ctx, pool, row))

	live, err := repo.ConsumeSession(ctx, pool, row.ID, later.Add(time.Second))
	require.NoError(t, err)
	assert.False(t, live)

	// A refused consumption is not a consumption: the stale row survives
	// until the sweep reaps it.
	_, err = repo.GetSession(ctx, pool, row.ID)
	require.NoError(t, err)

	reaped, err := repo.DeleteExpiredSessions(ctx, pool, later.Add(time.Second))
	require.NoError(t, err)
	assert.Equal(t, int64(1), reaped)
}
