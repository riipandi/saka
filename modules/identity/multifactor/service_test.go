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

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/pquerna/otp/totp"
	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/signin"
	"github.com/riipandi/tango/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// fakeIssuer records the sessions the completed sign-ins open, and knows
// exactly one account — the one the test seeded, the way the real issuer's
// lookup answers nothing for an identifier that names no row.
type fakeIssuer struct {
	opened []uuid.UUID
	known  uuid.UUID
}

func (f *fakeIssuer) IssueSession(_ context.Context, _ datastore.Querier, account *signin.Account, _, _ string, _ signin.SessionParams) (signin.Result, error) {
	f.opened = append(f.opened, account.ID)
	return signin.Result{SessionID: "sess_test", AccessToken: "at", RefreshToken: "rt"}, nil
}

func (f *fakeIssuer) FindAccountByIDAny(_ context.Context, id uuid.UUID) (*signin.Account, error) {
	if id != f.known {
		return nil, errors.New("fakeIssuer: unknown account")
	}
	return &signin.Account{ID: id, Username: "langdon", Email: "langdon@example.com"}, nil
}

// fakeNoticeEnqueuer records the removal notices the administrative disable
// queues.
type fakeNoticeEnqueuer struct {
	calls int
	last  MfaDisabledNotice
}

func (f *fakeNoticeEnqueuer) EnqueueMfaDisabledNotice(_ context.Context, notice MfaDisabledNotice) {
	f.calls++
	f.last = notice
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
	issuer.known = userID
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
	_, err = service.CompleteSignIn(ctx, outcome.PendingToken, "000000", nil, signin.SessionParams{})
	assert.ErrorIs(t, err, ErrCodeInvalid)

	result, err := service.CompleteSignIn(ctx, outcome.PendingToken, currentCode(begun.TotpID), nil, signin.SessionParams{})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{userID}, issuer.opened)
	assert.Equal(t, "sess_test", result.SessionID)

	// The bridge died with its success: the same token answers nothing.
	_, err = service.CompleteSignIn(ctx, outcome.PendingToken, currentCode(begun.TotpID), nil, signin.SessionParams{})
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
		_, err = service.CompleteSignIn(ctx, outcome.PendingToken, "000000", nil, signin.SessionParams{})
		assert.ErrorIs(t, err, ErrCodeInvalid)
	}
	_, err = service.CompleteSignIn(ctx, outcome.PendingToken, "000000", nil, signin.SessionParams{})
	assert.ErrorIs(t, err, ErrPendingExhausted)
	_, err = service.CompleteSignIn(ctx, outcome.PendingToken, currentCode(begun.TotpID), nil, signin.SessionParams{})
	assert.ErrorIs(t, err, ErrPendingInvalid)
	assert.Empty(t, issuer.opened, "the exhausted bridge opens nothing")

	// A fresh bridge answers the recovery code — the single-use guarantee
	// holds: the second attempt with the same code refuses.
	recoveryOutcome, err := service.BeginSignIn(ctx, userID, false)
	require.NoError(t, err)
	result, err := service.CompleteSignIn(ctx, recoveryOutcome.PendingToken, confirmed.RecoveryCodes[0], nil, signin.SessionParams{})
	require.NoError(t, err)
	assert.Equal(t, "sess_test", result.SessionID)

	thirdOutcome, err := service.BeginSignIn(ctx, userID, false)
	require.NoError(t, err)
	_, err = service.CompleteSignIn(ctx, thirdOutcome.PendingToken, confirmed.RecoveryCodes[0], nil, signin.SessionParams{})
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
	_, err = service.CompleteSignIn(ctx, oldOutcome.PendingToken, confirmed.RecoveryCodes[0], nil, signin.SessionParams{})
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

// The administrative disable: every factor goes without a proof — the
// operator's authority is the session, the notice rides the queue, and an
// account holding nothing answers the precondition refusal.
func TestAdminDisableMfaStripsEveryFactorWithoutAProof(t *testing.T) {
	service, _, userID, currentCode := mfaTestService(t)
	ctx := t.Context()

	notices := &fakeNoticeEnqueuer{}
	service.WithNoticeEnqueuer(notices)

	// An account without a confirmed factor: nothing to disable.
	err := service.AdminDisableMfa(ctx, userID, "cleanup")
	assert.ErrorIs(t, err, ErrNotConfirmed)
	assert.Equal(t, 0, notices.calls)

	begun, err := service.BeginTotpEnrollment(ctx, userID, "Phone")
	require.NoError(t, err)
	confirmed, err := service.ConfirmTotpEnrollment(ctx, userID, begun.TotpID, currentCode(begun.TotpID))
	require.NoError(t, err)

	// The disable takes no code: the holder has lost every factor, which is
	// the reason the procedure runs.
	require.NoError(t, service.AdminDisableMfa(ctx, userID, "lost device, verified over support"))
	owed, err := service.KeepsConfirmedFactor(ctx, userID)
	require.NoError(t, err)
	assert.False(t, owed)

	// The notice names the account and carries the operator's reason.
	assert.Equal(t, 1, notices.calls)
	assert.Equal(t, "langdon@example.com", notices.last.Email)
	assert.Equal(t, "lost device, verified over support", notices.last.Reason)

	// The recovery set went with the factors: a code from the old set
	// proves nothing anymore.
	consumed, err := service.consumeRecoveryCode(ctx, service.pool, userID, confirmed.RecoveryCodes[0], time.Now())
	require.NoError(t, err)
	assert.False(t, consumed)

	// Disabling twice is the precondition refusal, not a second notice.
	err = service.AdminDisableMfa(ctx, userID, "cleanup")
	assert.ErrorIs(t, err, ErrNotConfirmed)
	assert.Equal(t, 1, notices.calls)
}

// An unknown target answers the not-found the other administrative
// refusals keep.
func TestAdminDisableMfaRefusesAnUnknownAccount(t *testing.T) {
	service, _, _, _ := mfaTestService(t)

	err := service.AdminDisableMfa(t.Context(), uuid.NewV7(), "typo")
	assert.ErrorIs(t, err, ErrUserNotFound)
}

// The standalone proof: one code verifies once, the consumption is real,
// and the wrong shape refuses the same way the challenge does.
func TestVerifyRecoveryCodeSpendsOneCodeStandalone(t *testing.T) {
	service, _, userID, currentCode := mfaTestService(t)
	ctx := t.Context()

	// An account holding no recovery set refuses like any wrong code.
	err := service.VerifyRecoveryCode(ctx, userID, "AAAA-BBBB-CCCC")
	assert.ErrorIs(t, err, ErrCodeInvalid)

	begun, err := service.BeginTotpEnrollment(ctx, userID, "Phone")
	require.NoError(t, err)
	confirmed, err := service.ConfirmTotpEnrollment(ctx, userID, begun.TotpID, currentCode(begun.TotpID))
	require.NoError(t, err)

	// A wrong code refuses without spending anything.
	err = service.VerifyRecoveryCode(ctx, userID, "XXXX-YYYY-ZZZZ")
	assert.ErrorIs(t, err, ErrCodeInvalid)
	status, err := service.RecoveryCodesStatus(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, recoveryCodeCount, status.Unused)

	// The right code verifies — and is spent: it cannot verify twice, and
	// it cannot open a session with either.
	require.NoError(t, service.VerifyRecoveryCode(ctx, userID, confirmed.RecoveryCodes[0]))
	err = service.VerifyRecoveryCode(ctx, userID, confirmed.RecoveryCodes[0])
	assert.ErrorIs(t, err, ErrCodeInvalid)

	outcome, err := service.BeginSignIn(ctx, userID, false)
	require.NoError(t, err)
	_, err = service.CompleteSignIn(ctx, outcome.PendingToken, confirmed.RecoveryCodes[0], nil, signin.SessionParams{})
	assert.ErrorIs(t, err, ErrCodeInvalid)

	status, err = service.RecoveryCodesStatus(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, recoveryCodeCount-1, status.Unused)
}

// The listing's decrypted-secret aid: off by default, and on only where the
// wiring passes the development flag — the answer then names the same
// secret the enrollment began with.
func TestListTotpEnrollmentsCarriesTheSecretOnlyWhereTheAidRuns(t *testing.T) {
	service, _, userID, currentCode := mfaTestService(t)
	ctx := t.Context()

	begun, err := service.BeginTotpEnrollment(ctx, userID, "Phone")
	require.NoError(t, err)
	_, err = service.ConfirmTotpEnrollment(ctx, userID, begun.TotpID, currentCode(begun.TotpID))
	require.NoError(t, err)

	// The default listing names no secret: the enrollment's one answer was
	// the only channel the value traveled through.
	rows, err := service.ListTotpEnrollments(ctx, userID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Empty(t, rows[0].Secret)

	// The aid answers the secret verbatim — the same value the app holds.
	service.WithExposedSecrets(true)
	rows, err = service.ListTotpEnrollments(ctx, userID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, begun.Secret, rows[0].Secret)
}

// ---- helpers the test file owns ----

// migratedPool runs the migrations over a fresh container database. The
// signature matches the identity area's other suites.
func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "multifactor_test")
}

// insertUserBuilder answers the query and args one account row needs. The
// insert is hand-built here because the area's user repository belongs to
// its own package and the test needs exactly one row.
func insertUserBuilder(userID uuid.UUID, username string) (string, []any) {
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto("public.users")
	sb.Cols("id", "username", "email", "display_name", "first_name", "last_name", "disabled", "created_at", "updated_at")
	sb.Values(userID, username, "langdon@example.com", "Robert Langdon", "Robert", "Langdon", false, time.Now(), time.Now())
	return sb.Build()
}

// totpNow answers the code the secret renders now — the test's authenticator
// app, over pquerna's generator with the enrollment's fixed parameters.
func totpNow(secret string) (string, error) {
	return totp.GenerateCode(secret, time.Now())
}
