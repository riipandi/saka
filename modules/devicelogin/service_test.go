package devicelogin

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
	"github.com/riipandi/saka/pkg/testutils"
)

func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()
	return testutils.MigratedPostgres(t, "devicelogin_test")
}

func testService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()
	return NewService(pool, userService(t, pool), audit.NewRecorder(nil), "http://localhost:3080")
}

func userService(t *testing.T, pool *datastore.Postgres) *user.Service {
	t.Helper()
	return user.NewService(pool, audit.NewRecorder(nil), nil, nil)
}

func seedAccount(t *testing.T, pool *datastore.Postgres, username string) string {
	t.Helper()

	view, err := userService(t, pool).CreateUser(t.Context(), user.CreateParams{
		Username:    username,
		Email:       username + "@hogwarts.example",
		DisplayName: username,
	})
	require.NoError(t, err)
	return view.ID
}

// testCaller renders the wire-form caller a bearer token carries.
func testCaller(t *testing.T, wireID string) *jwtutils.Caller {
	t.Helper()
	return &jwtutils.Caller{UserID: wireID}
}

// disableAccount flips the row the direct write the other flows' tests
// use too.
func disableAccount(t *testing.T, pool *datastore.Postgres, wireID string) error {
	t.Helper()

	id, err := user.UUIDFromWire(wireID)
	if err != nil {
		return err
	}
	_, err = pool.Exec(t.Context(),
		`UPDATE public.users SET disabled = TRUE WHERE id = $1`, id)
	return err
}

func TestCreateOpensOnePairingRequestTheExchangeAccepts(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	created, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
	require.NoError(t, err)
	require.Len(t, created.UserCode, 9) // XXXX-XXXX
	require.Contains(t, created.UserCode, "-")
	require.Equal(t, pollInterval, created.Interval)
	require.Equal(t, "http://localhost:3080/device", created.VerificationURI)

	// The exchange answers pending until the decision lands.
	outcome, err := service.Exchange(t.Context(), created.ID, "token-plain")
	require.NoError(t, err)
	require.Equal(t, ExchangePending, outcome.Status)
}

func TestTheUserCodeSurvivesItsReadingForms(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	created, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
	require.NoError(t, err)

	// The hyphen and the case the entry may carry fold away.
	inspection, err := service.Inspect(t.Context(), created.UserCode)
	require.NoError(t, err)
	require.Equal(t, created.UserCode, inspection.UserCode)
	require.Equal(t, "192.0.2.10", inspection.IPAddress)
	require.Equal(t, "Probe/1.0", inspection.UserAgent)

	// The entry may arrive in any case: the fold takes it.
	_, err = service.Inspect(t.Context(), strings.ToLower(strings.ReplaceAll(created.UserCode, "-", "")))
	require.NoError(t, err)
}

func TestDecideStampsTheRowOnceAndTheExchangeConsumesIt(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userID := seedAccount(t, pool, "langdon")

	created, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
	require.NoError(t, err)

	caller := testCaller(t, userID)
	require.NoError(t, service.Decide(t.Context(), created.UserCode, DecisionApprove, caller))

	// A second decision answers the unknown: decided is decided forever.
	err = service.Decide(t.Context(), created.UserCode, DecisionApprove, caller)
	require.ErrorIs(t, err, ErrCodeUnknown)

	outcome, err := service.Exchange(t.Context(), created.ID, "token-plain")
	require.NoError(t, err)
	require.Equal(t, ExchangeDone, outcome.Status)
	// The exchange answers the row's raw UUID; the wire form is what the
	// account's other seams carry, so the shapes name the same account.
	resolved, err := service.LoadAccount(t.Context(), outcome.UserID)
	require.NoError(t, err)
	require.Equal(t, userID, resolved.ID)
	// The consumption is single-use: the second exchange reads nothing.
	_, err = service.Exchange(t.Context(), created.ID, "token-plain")
	require.ErrorIs(t, err, ErrCodeUnknown)
}

func TestADeniedRequestAnswersThePollWithNothing(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userID := seedAccount(t, pool, "neveu")

	created, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
	require.NoError(t, err)

	require.NoError(t, service.Decide(t.Context(), created.UserCode, DecisionDeny, testCaller(t, userID)))

	_, err = service.Exchange(t.Context(), created.ID, "token-plain")
	require.ErrorIs(t, err, ErrCodeUnknown)
}

func TestAWrongDeviceTokenAnswersNothing(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userID := seedAccount(t, pool, "vetra")

	created, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
	require.NoError(t, err)

	require.NoError(t, service.Decide(t.Context(), created.UserCode, DecisionApprove, testCaller(t, userID)))

	// The right id with the wrong pairing secret is the unknown: the
	// poll reads the row through the token's hash alone.
	_, err = service.Exchange(t.Context(), created.ID, "token-wrong")
	require.ErrorIs(t, err, ErrCodeUnknown)
}

func TestADisabledAccountCannotExchange(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)
	userID := seedAccount(t, pool, "sophie")

	created, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
	require.NoError(t, err)
	require.NoError(t, service.Decide(t.Context(), created.UserCode, DecisionApprove, testCaller(t, userID)))

	// Disable the account the approval named, the direct row write the
	// other flows' tests use too.
	require.NoError(t, disableAccount(t, pool, userID))

	_, err = service.LoadAccount(t.Context(), userID)
	require.ErrorIs(t, err, ErrCodeUnknown)
}

func TestAnExpiredRequestIsTheUnknown(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	created, err := service.Create(t.Context(), "token-plain", "192.0.2.10", "Probe/1.0")
	require.NoError(t, err)

	// Age the row past its window; the reads' expiry predicate does the
	// refusing. The created_at moves back with it, so the expiry check
	// constraint stays honest.
	_, err = pool.Exec(t.Context(),
		`UPDATE public.device_login_requests SET expires_at = $1, created_at = $2 WHERE id = $3`,
		time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(-6*time.Minute), created.ID)
	require.NoError(t, err)

	_, err = service.Inspect(t.Context(), created.UserCode)
	require.ErrorIs(t, err, ErrCodeUnknown)

	_, err = service.Exchange(t.Context(), created.ID, "token-plain")
	require.ErrorIs(t, err, ErrCodeUnknown)
}

func TestTheUserCodeAlphabetExcludesTheAmbiguous(t *testing.T) {
	for range 200 {
		code, err := newUserCode()
		require.NoError(t, err)
		require.Len(t, code, 8)
		for _, r := range code {
			require.NotContains(t, "0O1IL", string(r))
		}
	}
}
