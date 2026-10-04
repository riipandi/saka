package webauthn

import (
	"context"
	"strconv"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/testutils"
	"github.com/riipandi/saka/modules/identity/signin"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/jwtutils"
	conttest "github.com/riipandi/saka/pkg/testutils"
)

// testOrigin is the origin the ceremonies run against: the RP ID is its
// host, the way the production derivation reads app.base_url.
const testOrigin = "http://localhost:3080"

// fakeIssuer records the sessions the sign-ins opened and knows the
// accounts the tests seeded.
type fakeIssuer struct {
	accounts map[uuid.UUID]*signin.Account
	issued   []string // the providers the sessions opened under
}

func (f *fakeIssuer) FindAccountByID(_ context.Context, id uuid.UUID) (*signin.Account, error) {
	return f.findAny(id)
}

// FindAccountByIDAny is the same map the inner-join read answers: the fake
// cannot model the password join, so the stranding distinction lives in the
// test that deletes the map entry.
func (f *fakeIssuer) FindAccountByIDAny(_ context.Context, id uuid.UUID) (*signin.Account, error) {
	return f.findAny(id)
}

func (f *fakeIssuer) findAny(id uuid.UUID) (*signin.Account, error) {
	account, ok := f.accounts[id]
	if !ok {
		return nil, ErrNoRows
	}
	return account, nil
}

func (f *fakeIssuer) VerifyPassword(_ context.Context, id uuid.UUID, password string) (bool, error) {
	_, ok := f.accounts[id]
	if !ok {
		return false, ErrNoRows
	}
	return password == "expecto-patronum", nil
}

func (f *fakeIssuer) IssueSession(_ context.Context, _ datastore.Querier, account *signin.Account, provider, _ string, _ signin.SessionParams) (signin.Result, error) {
	f.issued = append(f.issued, provider)
	return signin.Result{
		AccessToken: "at",
		SessionID:   "sess_test",
		User:        signin.User{ID: account.ID.String(), Username: account.Username},
	}, nil
}

// fakeSettings answers the catalog's four keys with the values a test sets,
// and the unknown-setting sentinel for a key it does not carry — the state
// the fail-closed fallback reads.
type fakeSettings struct {
	values map[string]string
}

func (f *fakeSettings) Get(_ context.Context, key string) (string, error) {
	value, ok := f.values[key]
	if !ok {
		return "", errUnknownSetting
	}
	return value, nil
}

func (f *fakeSettings) GetString(ctx context.Context, key string) (string, error) {
	return f.Get(ctx, key)
}

func (f *fakeSettings) GetBool(ctx context.Context, key string) (bool, error) {
	value, err := f.Get(ctx, key)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

func (f *fakeSettings) GetInt64(ctx context.Context, key string) (int64, error) {
	value, err := f.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(value, 10, 64)
}

// webauthnTestService builds the service over a fresh database, the fake
// issuer, and the fake settings, answering the pieces a test drives.
func webauthnTestService(t *testing.T, values map[string]string) (*Service, *fakeIssuer, *fakeSettings) {
	t.Helper()
	conttest.SkipWithoutDocker(t)

	pool := testutils.MigratedPostgres(t, "webauthn_service_test")
	issuer := &fakeIssuer{accounts: map[uuid.UUID]*signin.Account{}}
	settings := &fakeSettings{values: values}
	cfg := config.Default()
	cfg.App.BaseURL = testOrigin
	service, err := NewService(cfg, pool, NewRepository(), issuer, settings,
		audit.NewRecorder(nil), nil)
	require.NoError(t, err)
	return service, issuer, settings
}

// defaultSettings is the catalog's defaults, spelled the way the catalog
// ships them.
func defaultSettings() map[string]string {
	return map[string]string{
		SettingAllowSyncedPasskeys: "true",
		SettingUserVerification:    UserVerificationRequired,
		SettingMaxCredentials:      "10",
		SettingMaxEnrollments:      "10",
	}
}

// seedAccount writes the account row and its password row — the FKs the
// credential rows carry and the join the fake issuer pretends to answer.
func seedAccount(t *testing.T, pool *datastore.Postgres, username string) (uuid.UUID, *signin.Account) {
	t.Helper()

	userID := uuid.NewV7()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.users (id, username, email, display_name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())`,
		userID, username, username+"@example.com", username)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`INSERT INTO public.user_passwords (user_id, password_hash) VALUES ($1, '$scrypt$test')`, userID)
	require.NoError(t, err)

	account := &signin.Account{ID: userID, Username: username, Email: username + "@example.com", DisplayName: username}
	return userID, account
}

// enroll walks the registration ceremony with the soft authenticator and
// answers the stored row's identifier.
func enroll(t *testing.T, service *Service, userID uuid.UUID, soft *SoftAuthenticator, name string) View {
	t.Helper()

	options, sessionID, err := service.BeginRegistration(t.Context(), userID)
	require.NoError(t, err)
	view, err := service.VerifyRegistration(t.Context(), userID, sessionID, soft.Create(t, options, testOrigin), name)
	require.NoError(t, err)
	return view
}

// mustCredentialUUID reads a view's wire identifier back into its UUID.
func mustCredentialUUID(t *testing.T, wire string) uuid.UUID {
	t.Helper()
	id, err := typeidParseCredential(wire)
	require.NoError(t, err)
	return id
}

// stepUpCaller answers the caller the interceptor carries: the wire-form
// subject the consume reads the account from.
func stepUpCaller(t *testing.T, userID uuid.UUID) *jwtutils.Caller {
	t.Helper()
	id, err := user.IDFromUUID(userID)
	require.NoError(t, err)
	return &jwtutils.Caller{UserID: id.String()}
}

// ---- enrollment ----

func TestEnrollmentEndsInACredential(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	pool := service.pool
	userID, account := seedAccount(t, pool, "hermione")
	issuer.accounts[userID] = account

	soft := NewSoftAuthenticator(false, false, true)
	view := enroll(t, service, userID, soft, "Test key")

	assert.NotEmpty(t, view.ID)
	assert.Equal(t, "Test key", view.Name)
	assert.False(t, view.BackupEligible, "a device-bound credential reports no backup eligibility")

	// The parsed attestation rests in the row: the counter the authenticator
	// reported at creation, and the AAGUID the view renders.
	row, err := service.repo.GetCredentialByID(t.Context(), pool, mustCredentialUUID(t, view.ID))
	require.NoError(t, err)
	assert.Equal(t, int64(0), row.SignCount)
	require.NotNil(t, row.AAGUID)
	assert.NotEmpty(t, *row.AAGUID)
}

func TestEnrollmentRefusesASyncedPasskeyWhenTheToggleIsOff(t *testing.T) {
	values := defaultSettings()
	values[SettingAllowSyncedPasskeys] = "false"
	service, issuer, _ := webauthnTestService(t, values)
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account

	options, sessionID, err := service.BeginRegistration(t.Context(), userID)
	require.NoError(t, err)
	soft := NewSoftAuthenticator(true, true, true) // backup-eligible: a synced passkey
	_, err = service.VerifyRegistration(t.Context(), userID, sessionID, soft.Create(t, options, testOrigin), "")
	assert.ErrorIs(t, err, ErrSyncedPasskeyOff)
}

func TestEnrollmentBeyondTheCredentialLimitIsRefused(t *testing.T) {
	values := defaultSettings()
	values[SettingMaxCredentials] = "1"
	service, issuer, _ := webauthnTestService(t, values)
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account

	// The first enroll takes the single slot; the second is refused at
	// verify, where the limit is judged — the ceremony the refusal costs is
	// the ceremony that tried to overfill.
	enroll(t, service, userID, NewSoftAuthenticator(false, false, true), "first")
	options, sessionID, err := service.BeginRegistration(t.Context(), userID)
	require.NoError(t, err)
	_, err = service.VerifyRegistration(t.Context(), userID, sessionID, NewSoftAuthenticator(false, false, true).Create(t, options, testOrigin), "second")
	assert.ErrorIs(t, err, ErrTooManyPasskeys)
}

// TestEnrollmentRefusesAnUnreadableLimit pins the fail-closed read: a limit
// that answers garbage refuses the enrollment rather than waving it through.
func TestEnrollmentRefusesAnUnreadableLimit(t *testing.T) {
	values := defaultSettings()
	values[SettingMaxCredentials] = "not-a-number"
	service, issuer, _ := webauthnTestService(t, values)
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account

	soft := NewSoftAuthenticator(false, false, true)
	options, sessionID, err := service.BeginRegistration(t.Context(), userID)
	require.NoError(t, err)
	_, err = service.VerifyRegistration(t.Context(), userID, sessionID, soft.Create(t, options, testOrigin), "key")
	assert.ErrorIs(t, err, ErrSettingUnreadable)
}

// ---- the administrative roll ----

// accountWire answers the account's wire form, the way an admin request
// carries it.
func accountWire(t *testing.T, userID uuid.UUID) string {
	t.Helper()
	id, err := user.IDFromUUID(userID)
	require.NoError(t, err)
	return id.String()
}

func TestAdminSeesAnotherAccountsRoll(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account

	enroll(t, service, userID, NewSoftAuthenticator(false, false, true), "Admin eye")
	enroll(t, service, userID, NewSoftAuthenticator(false, false, true), "")

	roll, err := service.AdminListCredentials(t.Context(), accountWire(t, userID))
	require.NoError(t, err)
	require.Len(t, roll, 2)
	assert.Equal(t, "Admin eye", roll[0].Name)
	// The unnamed enrollment answers the authenticator model's display name.
	assert.NotEmpty(t, roll[1].Name)
}

func TestAdminRefusesAMalformedAccountID(t *testing.T) {
	service, _, _ := webauthnTestService(t, defaultSettings())

	// A wire form that does not parse names no account; a valid one that
	// holds no credentials answers an empty roll, the same shape the
	// holder's own list answers before the first enrollment.
	_, err := service.AdminListCredentials(t.Context(), "not-a-user-id")
	assert.ErrorIs(t, err, ErrAccountUnknown)
}

func TestAdminRenamesAnotherAccountsPasskey(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account
	enrolled := enroll(t, service, userID, NewSoftAuthenticator(false, false, true), "old name")

	renamed, err := service.AdminRenameCredential(t.Context(), accountWire(t, userID), enrolled.ID, "operator rename")
	require.NoError(t, err)
	assert.Equal(t, "operator rename", renamed.Name)

	// The holder's own view carries the rename too — one row, two doors.
	roll, err := service.ListCredentials(t.Context(), userID)
	require.NoError(t, err)
	assert.Equal(t, "operator rename", roll[0].Name)
}

func TestAdminDeleteRemovesAndAudits(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account
	first := enroll(t, service, userID, NewSoftAuthenticator(false, false, true), "first")
	second := enroll(t, service, userID, NewSoftAuthenticator(false, false, true), "second")

	require.NoError(t, service.AdminDeleteCredential(t.Context(), accountWire(t, userID), first.ID))

	roll, err := service.ListCredentials(t.Context(), userID)
	require.NoError(t, err)
	require.Len(t, roll, 1)
	assert.Equal(t, second.ID, roll[0].ID)
}

func TestAdminDeleteRefusesTheLastWayIn(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account
	enrolled := enroll(t, service, userID, NewSoftAuthenticator(false, false, true), "only")

	// The account holds the password row, so the removal is allowed — the
	// password is the way back in. The stranding refusal is pinned here at
	// the administrator's door too: the issuer's read joins the password
	// table, so an account whose password row is gone answers not-found,
	// and the administrator's key must not waive the recovery anchor.
	_, err := service.pool.Exec(t.Context(), `DELETE FROM public.user_passwords WHERE user_id = $1`, userID)
	require.NoError(t, err)
	delete(issuer.accounts, userID) // the way the issuer sees it, the account's way in is gone
	assert.ErrorIs(t, service.AdminDeleteCredential(t.Context(), accountWire(t, userID), enrolled.ID), ErrLastWayIn)
}

// ---- sign-in ----

func TestSignInEndsInAWholeSession(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account

	soft := NewSoftAuthenticator(false, false, true)
	enroll(t, service, userID, soft, "key")

	options, sessionID, err := service.BeginLogin(t.Context())
	require.NoError(t, err)
	result, err := service.VerifyLogin(t.Context(), sessionID, soft.Get(t, options, testOrigin, userID[:]), signin.SessionParams{})
	require.NoError(t, err)
	assert.Equal(t, "at", result.AccessToken)
	assert.Equal(t, []string{signin.ProviderWebauthn}, issuer.issued, "the session opened under the passkey provider, whole — no bridge, no second factor")

	// The assertion's bookkeeping rests: the counter advanced, the
	// last-use stamp is set.
	row, err := service.repo.ListCredentials(t.Context(), service.pool, userID)
	require.NoError(t, err)
	require.Len(t, row, 1)
	assert.Equal(t, int64(1), row[0].SignCount)
	require.NotNil(t, row[0].LastUsedAt)
}

func TestAReplayedCeremonyHandleIsRefused(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account

	soft := NewSoftAuthenticator(false, false, true)
	enroll(t, service, userID, soft, "key")

	options, sessionID, err := service.BeginLogin(t.Context())
	require.NoError(t, err)
	assertion := soft.Get(t, options, testOrigin, userID[:])
	_, err = service.VerifyLogin(t.Context(), sessionID, assertion, signin.SessionParams{})
	require.NoError(t, err)

	_, err = service.VerifyLogin(t.Context(), sessionID, assertion, signin.SessionParams{})
	assert.ErrorIs(t, err, ErrCeremonyInvalid, "the handle was spent; a replay names no row")
}

// TestAClonedCredentialIsRefused pins the counter regression the stored
// sign_count makes visible: a credential whose counter goes backwards is
// cloned, and the assertion records nothing.
func TestAClonedCredentialIsRefused(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account

	soft := NewSoftAuthenticator(false, false, true)
	enroll(t, service, userID, soft, "key")

	options, sessionID, err := service.BeginLogin(t.Context())
	require.NoError(t, err)
	_, err = service.VerifyLogin(t.Context(), sessionID, soft.Get(t, options, testOrigin, userID[:]), signin.SessionParams{})
	require.NoError(t, err)

	// The clone rewinds the counter: the same key, an assertion that claims
	// less use than the row remembers.
	soft.RewindCounter()
	options, sessionID, err = service.BeginLogin(t.Context())
	require.NoError(t, err)
	_, err = service.VerifyLogin(t.Context(), sessionID, soft.Get(t, options, testOrigin, userID[:]), signin.SessionParams{})
	assert.ErrorIs(t, err, ErrClonedCredential)

	row, err := service.repo.ListCredentials(t.Context(), service.pool, userID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), row[0].SignCount, "the refused assertion advanced nothing")
}

func TestADuplicateEnrollmentIsRefusedAsAPrecondition(t *testing.T) {
	service, _, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	service.issuer = &passwordIssuer{accounts: map[uuid.UUID]*signin.Account{userID: account}}

	soft := NewSoftAuthenticator(false, false, true)
	options, sessionID, err := service.BeginRegistration(t.Context(), userID)
	require.NoError(t, err)
	first := soft.Create(t, options, testOrigin)
	_, err = service.VerifyRegistration(t.Context(), userID, sessionID, first, "key")
	require.NoError(t, err)

	// The same authenticator walks a second ceremony: the key's identifier
	// is the protocol's uniqueness, and the insert answers the duplicate
	// refusal — not the internal failure a bare constraint violation was.
	options, sessionID, err = service.BeginRegistration(t.Context(), userID)
	require.NoError(t, err)
	duplicate := soft.Create(t, options, testOrigin)
	_, err = service.VerifyRegistration(t.Context(), userID, sessionID, duplicate, "key")
	assert.ErrorIs(t, err, ErrCredentialDuplicate)
}

// ---- step-up ----

func TestAStepUpProofSpendsOnce(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account

	soft := NewSoftAuthenticator(false, false, true)
	enroll(t, service, userID, soft, "key")

	// The password proof mints the token.
	token, expiresAt, err := service.Reauthenticate(t.Context(), userID, "expecto-patronum", "", "", "")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.True(t, expiresAt.After(time.Now()))

	// The passkey proof mints one too, over its own ceremony.
	options, sessionID, err := service.BeginLogin(t.Context())
	require.NoError(t, err)
	_, _, err = service.Reauthenticate(t.Context(), userID, "", sessionID, soft.Get(t, options, testOrigin, userID[:]), "")
	require.NoError(t, err)

	// Each token spends exactly once.
	caller := stepUpCaller(t, userID)
	require.NoError(t, service.ConsumeReauthentication(t.Context(), caller, token))
	assert.ErrorIs(t, service.ConsumeReauthentication(t.Context(), caller, token), ErrProofRefused)
}

func TestAForeignAssertionIsNotAProof(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account
	other, otherAccount := seedAccount(t, service.pool, "langdon")
	issuer.accounts[other] = otherAccount

	// Langdon's credential, Hermione's session: the assertion proves
	// Langdon, so for Hermione it is not a proof.
	soft := NewSoftAuthenticator(false, false, true)
	enroll(t, service, other, soft, "langdon's key")

	options, sessionID, err := service.BeginLogin(t.Context())
	require.NoError(t, err)
	_, _, err = service.Reauthenticate(t.Context(), userID, "", sessionID, soft.Get(t, options, testOrigin, other[:]), "")
	assert.ErrorIs(t, err, ErrProofRefused)
}

func TestAWrongPasswordIsNotAProof(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account

	_, _, err := service.Reauthenticate(t.Context(), userID, "marauder", "", "", "")
	assert.ErrorIs(t, err, ErrProofRefused)
}

// The step-up window is the `session.reverification_window` setting read at
// grant time: the token's expiry sits exactly one window out, a proof spent
// inside it answers, and the same proof presented after the window has
// passed is dead on arrival — no fresh grant in between.
func TestAStepUpProofLivesInTheReverificationWindow(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, map[string]string{
		SettingAllowSyncedPasskeys:  "true",
		SettingUserVerification:     UserVerificationRequired,
		SettingMaxCredentials:       "10",
		SettingMaxEnrollments:       "10",
		SettingReverificationWindow: "120", // two minutes
	})
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account

	soft := NewSoftAuthenticator(false, false, true)
	enroll(t, service, userID, soft, "key")

	// The window the setting names, not the constant the service used to
	// carry: the grant's expiry sits exactly two minutes out. The clock is
	// held still so the two reads share one instant.
	at := time.Now()
	service.now = func() time.Time { return at }
	token, expiresAt, err := service.Reauthenticate(t.Context(), userID, "expecto-patronum", "", "", "")
	require.NoError(t, err)
	assert.Equal(t, 2*time.Minute, expiresAt.Sub(at))

	// Inside the window the proof spends.
	require.NoError(t, service.ConsumeReauthentication(t.Context(), stepUpCaller(t, userID), token))

	// Past the window a fresh grant is dead on arrival: the proof was
	// confirmed at grant time, and no consumption may outlive the window.
	token, _, err = service.Reauthenticate(t.Context(), userID, "expecto-patronum", "", "", "")
	require.NoError(t, err)
	service.now = func() time.Time { return at.Add(3 * time.Minute) }
	assert.ErrorIs(t, service.ConsumeReauthentication(t.Context(), stepUpCaller(t, userID), token), ErrProofRefused)
}

// A deployment whose settings feature cannot answer the window grants the
// catalog default — thirty minutes — rather than refusing every proof.
func TestTheReverificationWindowFallsBackToTheDefault(t *testing.T) {
	service, issuer, _ := webauthnTestService(t, defaultSettings())
	userID, account := seedAccount(t, service.pool, "hermione")
	issuer.accounts[userID] = account

	at := time.Now()
	service.now = func() time.Time { return at }
	_, expiresAt, err := service.Reauthenticate(t.Context(), userID, "expecto-patronum", "", "", "")
	require.NoError(t, err)
	assert.Equal(t, reverificationWindowDefault, expiresAt.Sub(at))
}
