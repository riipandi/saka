package multifactor

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/pquerna/otp/totp"
	"github.com/riipandi/tango/database"
	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/signin"
	"github.com/riipandi/tango/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// testCipher is the sealing a test runs: a real AES-256-GCM over a key the
// test owns, so the round trip through the row is the production shape.
type testCipher struct {
	gcm cipher.AEAD
}

func newTestCipher(t *testing.T) *testCipher {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	return &testCipher{gcm: gcm}
}

func (c *testCipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "enc:" + hex.EncodeToString(c.gcm.Seal(nonce, nonce, []byte(plaintext), nil)), nil
}

func (c *testCipher) Decrypt(encoded string) (string, error) {
	const prefix = "enc:"
	if len(encoded) < len(prefix)+c.gcm.NonceSize()*2 {
		return "", errors.New("testCipher: too short")
	}
	raw, err := hex.DecodeString(encoded[len(prefix):])
	if err != nil {
		return "", err
	}
	nonce, body := raw[:c.gcm.NonceSize()], raw[c.gcm.NonceSize():]
	plain, err := c.gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// fakeIssuer records the sessions the completed sign-ins open.
type fakeIssuer struct {
	opened []uuid.UUID
}

func (f *fakeIssuer) IssueSession(_ context.Context, _ datastore.Querier, account *signin.Account, _, _ string, _ signin.SessionParams) (signin.Result, error) {
	f.opened = append(f.opened, account.ID)
	return signin.Result{SessionID: "sess_test", AccessToken: "at", RefreshToken: "rt"}, nil
}

func (f *fakeIssuer) FindAccountByID(_ context.Context, id uuid.UUID) (*signin.Account, error) {
	return &signin.Account{ID: id, Username: "langdon", Email: "langdon@example.com"}, nil
}

// mfaTestService builds the service over a fresh database and a seeded
// account, answering the pieces a test drives.
func mfaTestService(t *testing.T) (*Service, *fakeIssuer, uuid.UUID, func(code string) string) {
	t.Helper()
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	issuer := &fakeIssuer{}
	service := NewService(pool, newTestCipher(t), issuer, audit.NewRecorder(nil), "Tango Test", nil)

	// The account the ceremonies run on.
	userID := uuid.NewV7()
	insertQuery, insertArgs := insertUserBuilder(userID, "langdon")
	if _, err := pool.Exec(t.Context(), insertQuery, insertArgs...); err != nil {
		require.NoError(t, err)
	}

	// currentCode turns a totp_id into the code the secret answers now —
	// the app's stand-in in every test.
	currentCode := func(totpID string) string {
		row, err := service.ownedEnrollment(t.Context(), userID, totpID)
		require.NoError(t, err)
		secret, err := service.unseal(row.Secret)
		require.NoError(t, err)
		code, err := totpNow(secret)
		require.NoError(t, err)
		return code
	}

	return service, issuer, userID, currentCode
}

// The enrollment ceremony: begin answers the secret once, a wrong code does
// not confirm, and the right one does — with the recovery set riding the
// account's first confirmation.
func TestConfirmTotpEnrollmentActivatesOnTheRightCode(t *testing.T) {
	service, _, userID, currentCode := mfaTestService(t)
	ctx := t.Context()

	begun, err := service.BeginTotpEnrollment(ctx, userID, "Phone")
	require.NoError(t, err)
	assert.NotEmpty(t, begun.Secret)
	assert.Contains(t, begun.OTPAuthURI, "otpauth://totp/")

	_, err = service.ConfirmTotpEnrollment(ctx, userID, begun.TotpID, "000000")
	assert.ErrorIs(t, err, ErrCodeInvalid)

	confirmed, err := service.ConfirmTotpEnrollment(ctx, userID, begun.TotpID, currentCode(begun.TotpID))
	require.NoError(t, err)
	assert.Len(t, confirmed.RecoveryCodes, recoveryCodeCount)

	// The set is one per account, not one per device: the second enrollment
	// answers no codes.
	second, err := service.BeginTotpEnrollment(ctx, userID, "Tablet")
	require.NoError(t, err)
	again, err := service.ConfirmTotpEnrollment(ctx, userID, second.TotpID, currentCode(second.TotpID))
	require.NoError(t, err)
	assert.Empty(t, again.RecoveryCodes)
}

// The challenge: the pending bridge opens the session on the app's code, the
// same code cannot open two sessions, and a wrong code eats the bridge's
// failure budget.
func TestCompleteSignInOpensTheSessionOncePerCode(t *testing.T) {
	service, issuer, userID, currentCode := mfaTestService(t)
	ctx := t.Context()

	begun, err := service.BeginTotpEnrollment(ctx, userID, "Phone")
	require.NoError(t, err)
	_, err = service.ConfirmTotpEnrollment(ctx, userID, begun.TotpID, currentCode(begun.TotpID))
	require.NoError(t, err)

	outcome, err := service.BeginSignIn(ctx, userID, false)
	require.NoError(t, err)

	// A wrong code refuses but keeps the bridge alive.
	_, err = service.CompleteSignIn(ctx, outcome.PendingToken, "000000", signin.SessionParams{})
	assert.ErrorIs(t, err, ErrCodeInvalid)

	result, err := service.CompleteSignIn(ctx, outcome.PendingToken, currentCode(begun.TotpID), signin.SessionParams{})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{userID}, issuer.opened)
	assert.Equal(t, "sess_test", result.SessionID)

	// The bridge died with its success: the same token answers nothing.
	_, err = service.CompleteSignIn(ctx, outcome.PendingToken, currentCode(begun.TotpID), signin.SessionParams{})
	assert.ErrorIs(t, err, ErrPendingInvalid)
}

// The budget: a bridge dies after its wrong codes, and the recovery code is
// the way in when the device is gone.
func TestCompleteSignInExhaustsTheBudgetAndRecoveryCodesStandIn(t *testing.T) {
	service, issuer, userID, currentCode := mfaTestService(t)
	ctx := t.Context()

	begun, err := service.BeginTotpEnrollment(ctx, userID, "Phone")
	require.NoError(t, err)
	confirmed, err := service.ConfirmTotpEnrollment(ctx, userID, begun.TotpID, currentCode(begun.TotpID))
	require.NoError(t, err)

	outcome, err := service.BeginSignIn(ctx, userID, false)
	require.NoError(t, err)
	// The budget's last wrong code is the one that kills the bridge: the
	// earlier ones refuse as invalid codes, the budget's edge refuses as
	// exhausted, and anything after answers nothing at all.
	for range maxAttempts - 1 {
		_, err = service.CompleteSignIn(ctx, outcome.PendingToken, "000000", signin.SessionParams{})
		assert.ErrorIs(t, err, ErrCodeInvalid)
	}
	_, err = service.CompleteSignIn(ctx, outcome.PendingToken, "000000", signin.SessionParams{})
	assert.ErrorIs(t, err, ErrPendingExhausted)
	_, err = service.CompleteSignIn(ctx, outcome.PendingToken, currentCode(begun.TotpID), signin.SessionParams{})
	assert.ErrorIs(t, err, ErrPendingInvalid)
	assert.Empty(t, issuer.opened, "the exhausted bridge opens nothing")

	// A fresh bridge answers the recovery code — the single-use guarantee
	// holds: the second attempt with the same code refuses.
	recoveryOutcome, err := service.BeginSignIn(ctx, userID, false)
	require.NoError(t, err)
	result, err := service.CompleteSignIn(ctx, recoveryOutcome.PendingToken, confirmed.RecoveryCodes[0], signin.SessionParams{})
	require.NoError(t, err)
	assert.Equal(t, "sess_test", result.SessionID)

	thirdOutcome, err := service.BeginSignIn(ctx, userID, false)
	require.NoError(t, err)
	_, err = service.CompleteSignIn(ctx, thirdOutcome.PendingToken, confirmed.RecoveryCodes[0], signin.SessionParams{})
	assert.ErrorIs(t, err, ErrCodeInvalid)
}

// The proofs: regenerate answers a fresh set once, the old codes are dead,
// and disable strips every factor after the code.
func TestRegenerateAndDisableRequireTheSecondFactor(t *testing.T) {
	service, _, userID, currentCode := mfaTestService(t)
	ctx := t.Context()

	begun, err := service.BeginTotpEnrollment(ctx, userID, "Phone")
	require.NoError(t, err)
	confirmed, err := service.ConfirmTotpEnrollment(ctx, userID, begun.TotpID, currentCode(begun.TotpID))
	require.NoError(t, err)

	// The wrong code is refused everywhere.
	_, err = service.RegenerateRecoveryCodes(ctx, userID, "000000")
	assert.ErrorIs(t, err, ErrCodeInvalid)

	fresh, err := service.RegenerateRecoveryCodes(ctx, userID, currentCode(begun.TotpID))
	require.NoError(t, err)
	assert.Len(t, fresh, recoveryCodeCount)

	// The old set died with the regenerate.
	oldOutcome, err := service.BeginSignIn(ctx, userID, false)
	require.NoError(t, err)
	_, err = service.CompleteSignIn(ctx, oldOutcome.PendingToken, confirmed.RecoveryCodes[0], signin.SessionParams{})
	assert.ErrorIs(t, err, ErrCodeInvalid)

	// Disable with the fresh set, then the account is one factor again.
	require.NoError(t, service.DisableMfa(ctx, userID, fresh[0]))
	owed, err := service.KeepsConfirmedFactor(ctx, userID)
	require.NoError(t, err)
	assert.False(t, owed)

	// A disabled account holds no proof to give: the destructive procedures
	// refuse before the code runs.
	_, err = service.RegenerateRecoveryCodes(ctx, userID, fresh[1])
	assert.ErrorIs(t, err, ErrNotConfirmed)
	require.ErrorIs(t, service.DisableMfa(ctx, userID, fresh[1]), ErrNotConfirmed)
}

// A factor's removal that would disarm the account demands the proof.
func TestDeleteTotpEnrollmentProvesTheLastRemoval(t *testing.T) {
	service, _, userID, currentCode := mfaTestService(t)
	ctx := t.Context()

	begun, err := service.BeginTotpEnrollment(ctx, userID, "Phone")
	require.NoError(t, err)
	_, err = service.ConfirmTotpEnrollment(ctx, userID, begun.TotpID, currentCode(begun.TotpID))
	require.NoError(t, err)

	// The unconfirmed second row is a mistyped start: no proof, just a
	// delete.
	draft, err := service.BeginTotpEnrollment(ctx, userID, "Mistyped")
	require.NoError(t, err)
	require.NoError(t, service.DeleteTotpEnrollment(ctx, userID, draft.TotpID, ""))

	// The last confirmed factor's removal needs the code.
	require.ErrorIs(t, service.DeleteTotpEnrollment(ctx, userID, begun.TotpID, ""), ErrProofRequired)
	require.ErrorIs(t, service.DeleteTotpEnrollment(ctx, userID, begun.TotpID, "000000"), ErrCodeInvalid)

	// With the code, the factor and its recovery set go together.
	require.NoError(t, service.DeleteTotpEnrollment(ctx, userID, begun.TotpID, currentCode(begun.TotpID)))
	owed, err := service.KeepsConfirmedFactor(ctx, userID)
	require.NoError(t, err)
	assert.False(t, owed)
}

// ---- helpers the test file owns ----

// migratedPool runs the migrations over a fresh container database. The
// signature matches the identity area's other suites.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)

	migrationDB, err := datastore.OpenMigrationDB(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	migrator, err := database.NewMigrator(t.Context(), migrationDB, database.MigratorOptions{})
	require.NoError(t, err)
	_, err = migrator.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, migrationDB.Close())

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             dsn,
		ApplicationName: "multifactor_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(t.Context()) })
	return pool
}

// insertUserBuilder answers the query and args one account row needs. The
// insert is hand-built here because the area's user repository belongs to
// its own package and the test needs exactly one row.
func insertUserBuilder(userID uuid.UUID, username string) (string, []any) {
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto("public.users")
	sb.Cols("id", "username", "email", "display_name", "first_name", "last_name", "is_admin", "disabled", "created_at", "updated_at")
	sb.Values(userID, username, "langdon@example.com", "Robert Langdon", "Robert", "Langdon", false, false, time.Now(), time.Now())
	return sb.Build()
}

// totpNow answers the code the secret renders now — the test's authenticator
// app, over pquerna's generator with the enrollment's fixed parameters.
func totpNow(secret string) (string, error) {
	return totp.GenerateCode(secret, time.Now())
}
