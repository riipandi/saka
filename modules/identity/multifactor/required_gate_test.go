package multifactor

import (
	"context"
	"errors"
	"testing"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/modules/identity/signin"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/testutils"
)

// The `mfa.required` composition: the real sign-in issuer and the real
// multifactor service over one pool, wired the way the identity area wires
// them. The unit tests above fake the issuer; this one proves the fork the
// composition root serves — a factor-less password success routes to
// enrollment, no token is minted until a factor is confirmed, and the
// confirmed factor completes the same sign-in the challenge path completes.

const gateTestSecretHex = "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"

// gateSettings answers the two keys the sign-in fork reads: the gate is on,
// the session bound is the catalog default.
type gateSettings struct{}

func (gateSettings) GetInt64(_ context.Context, _ string) (int64, error) {
	return 604800, nil
}

func (gateSettings) GetString(_ context.Context, _ string) (string, error) {
	return "", errors.New("no string settings in this test")
}

func (gateSettings) GetBool(_ context.Context, key string) (bool, error) {
	if key == signin.SettingMFARequired {
		return true, nil
	}
	return false, nil
}

// gateTestService builds both services over one pool and one seeded account:
// a password the tests know, an address a code has confirmed, no factor.
func gateTestService(t *testing.T) (*signin.Service, *Service, uuid.UUID, *datastore.Postgres) {
	t.Helper()
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)

	cfg := config.Default()
	cfg.Auth.SecretKey = gateTestSecretHex
	recorder := audit.NewRecorder(nil)

	signinService := signin.NewService(cfg, pool, signin.NewRepository(pool),
		jwks.NewService(cfg, nil, nil, nil), recorder, nil)
	mfaService := NewService(pool, newTestCipher(t), signinService, recorder, "Saka Test", nil)
	signinService.WithMFAGate(mfaService).WithSessionSettings(gateSettings{})

	// The account the fork runs on: a real password hash, the verification
	// column stamped, no authenticator of any kind.
	userID := uuid.NewV7()
	insertQuery, insertArgs := gateUserBuilder(userID)
	_, err := pool.Exec(t.Context(), insertQuery, insertArgs...)
	require.NoError(t, err)
	hash, err := crypto.NewPasswordHasher().Hash("expecto-patronum")
	require.NoError(t, err)
	pb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	pb.InsertInto(entity.TableUserPasswords)
	pb.Cols("user_id", "password_hash")
	pb.Values(userID, hash)
	passwordQuery, passwordArgs := pb.Build()
	_, err = pool.Exec(t.Context(), passwordQuery, passwordArgs...)
	require.NoError(t, err)

	return signinService, mfaService, userID, pool
}

// gateUserBuilder writes the account row the way a verified sign-up leaves
// it — the columns the sign-in's own gates read before the fork runs.
func gateUserBuilder(userID uuid.UUID) (string, []any) {
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(entity.TableUsers)
	sb.Cols("id", "username", "email", "display_name", "disabled", "email_verified_at")
	sb.Values(userID, "langdon", "langdon@example.com", "Robert Langdon", false, time.Now())
	return sb.Build()
}

// The gate's fork, end to end: the first sign-in of a factor-less account
// answers the enrollment bridge and no token; the enrollment the bridge
// admits turns the same account into a challenged one; the challenge
// completes into the token pair.
func TestTheRequiredGateRoutesAFactorlessSignInToEnrollment(t *testing.T) {
	signinService, mfaService, userID, pool := gateTestService(t)
	ctx := t.Context()

	// First sign-in: the password is proven, the account keeps no factor,
	// and the gate is on — the enrollment bridge is the whole answer.
	result, err := signinService.SignIn(ctx, signin.Params{
		Identity: "langdon@example.com", Password: "expecto-patronum",
		UserAgent: "gate_test/1", IPAddress: "192.0.2.10",
	})
	require.NoError(t, err)
	assert.True(t, result.MFARequired)
	assert.True(t, result.MFAEnrollmentRequired)
	assert.Empty(t, result.AccessToken)
	assert.Empty(t, result.RefreshToken)
	assert.NotEmpty(t, result.MFAPendingToken)

	// The bridge admits the enrollment endpoints: resolved, begun, and
	// confirmed — the confirm answers the recovery set once.
	enrolled, err := mfaService.ResolveEnrollmentBridge(ctx, result.MFAPendingToken)
	require.NoError(t, err)
	assert.Equal(t, userID, enrolled)
	begun, err := mfaService.BeginTotpEnrollment(ctx, enrolled, "Phone")
	require.NoError(t, err)
	confirmed, err := mfaService.ConfirmTotpEnrollment(ctx, enrolled, begun.TotpID, currentCodeOf(t, mfaService, enrolled, begun.TotpID))
	require.NoError(t, err)
	assert.NotEmpty(t, confirmed.RecoveryCodes)

	// The second sign-in of the now one-factor account answers the
	// challenge, not a second enrollment.
	result, err = signinService.SignIn(ctx, signin.Params{
		Identity: "langdon@example.com", Password: "expecto-patronum",
		UserAgent: "gate_test/1", IPAddress: "192.0.2.10",
	})
	require.NoError(t, err)
	assert.True(t, result.MFARequired)
	assert.False(t, result.MFAEnrollmentRequired)

	// The challenge completes into the token pair the session owns.
	completed, err := mfaService.CompleteSignIn(ctx, result.MFAPendingToken,
		currentCodeOf(t, mfaService, userID, begun.TotpID), nil, signin.SessionParams{})
	require.NoError(t, err)
	assert.NotEmpty(t, completed.AccessToken)
	assert.NotEmpty(t, completed.RefreshToken)

	// The enrollment bridge itself is spent by nothing the caller holds
	// once the factor stands — and a verify-purpose bridge never reaches
	// the enrollment endpoints: the challenge bridge of the second sign-in
	// answers the one refusal the resolver knows.
	_, err = mfaService.ResolveEnrollmentBridge(ctx, result.MFAPendingToken)
	assert.ErrorIs(t, err, ErrPendingInvalid)

	// The rows the flow left behind: one confirmed factor, no pending
	// bridge of either purpose the account could reuse.
	count, err := NewRepository().CountConfirmedTotp(ctx, pool, userID)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// currentCodeOf reads the enrollment's secret through the service's own
// unsealer and answers the code the authenticator would render now.
func currentCodeOf(t *testing.T, service *Service, userID uuid.UUID, totpID string) string {
	t.Helper()
	row, err := service.ownedEnrollment(t.Context(), userID, totpID)
	require.NoError(t, err)
	secret, err := service.unseal(row.Secret)
	require.NoError(t, err)
	code, err := totpNow(secret)
	require.NoError(t, err)
	return code
}
