package onetimeaccess

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"log/slog"
	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/jobs"
	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/modules/identity/jwks"
	"github.com/riipandi/tango/modules/identity/multifactor"
	"github.com/riipandi/tango/modules/identity/signin"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/testutils"
)

// testSecretHex is the HMAC secret the tests sign with: any 32-byte hex
// value, the same one the sign-in tests use.
const testSecretHex = "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"

func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "onetimeaccess_test")
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Auth.SecretKey = testSecretHex
	return cfg
}

// testService builds the service over the real sign-in issuer and a queue
// whose processors are registered but whose workers never start: an enqueue
// lands in the table and stays pending, which is what the assertions read.
// The two email switches are the arguments, because the paths refuse
// separately.
func testService(t *testing.T, pool *datastore.Postgres, adminEmail, publicEmail bool) *Service {
	t.Helper()

	cfg := testConfig()
	cfg.Auth.OneTimeAccessEmailAsAdminEnabled = adminEmail
	cfg.Auth.OneTimeAccessEmailAsUnauthenticatedEnabled = publicEmail
	// Any host makes the mailer report configured; no connection is dialed
	// until a message is submitted.
	cfg.Mailer.SMTPHost = "localhost"
	mail, err := mailerService(cfg)
	require.NoError(t, err)

	client, err := queue.NewClient(queue.ClientConfig{
		Store:        pool,
		NumWorkers:   1,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)
	jobs.Register(client, time.Hour, nil, mail, pool, "http://localhost:3000", false, true, nil, nil, nil, nil, nil)

	issuer := signin.NewService(testConfig(), pool, signin.NewRepository(pool),
		jwks.NewService(testConfig(), nil, nil, nil), nil, nil)
	return NewService(cfg, pool, issuer, audit.NewRecorder(slog.New(slog.DiscardHandler)), mail, client, nil)
}

// testServiceWithMfa builds the service with the real second factor wired
// behind the issuer's gate — the wiring the identity area performs — so a
// test can prove the exchange asks the fork's question.
func testServiceWithMfa(t *testing.T, pool *datastore.Postgres) (*Service, *multifactor.Service, *signin.Service) {
	t.Helper()

	cfg := testConfig()
	cfg.Mailer.SMTPHost = "localhost"
	mail, err := mailerService(cfg)
	require.NoError(t, err)

	client, err := queue.NewClient(queue.ClientConfig{
		Store:        pool,
		NumWorkers:   1,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)
	jobs.Register(client, time.Hour, nil, mail, pool, "http://localhost:3000", false, true, nil, nil, nil, nil, nil)

	cfg.Auth.SecretKey = testSecretHex
	issuer := signin.NewService(cfg, pool, signin.NewRepository(pool),
		jwks.NewService(cfg, nil, nil, nil), audit.NewRecorder(slog.New(slog.DiscardHandler)), nil)
	mfa := multifactor.NewService(pool, testSealer(t), issuer,
		audit.NewRecorder(slog.New(slog.DiscardHandler)), "Tango", nil)
	issuer.WithMFAGate(mfa)
	return NewService(cfg, pool, issuer, audit.NewRecorder(slog.New(slog.DiscardHandler)), mail, client, nil), mfa, issuer
}

// testSealer is the sealing a test runs: a real AES-256-GCM over a key the
// test owns, so the round trip through the row is the production shape.
func testSealer(t *testing.T) *testAESCipher {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	return &testAESCipher{gcm: gcm}
}

type testAESCipher struct{ gcm cipher.AEAD }

func (c *testAESCipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "enc:" + hex.EncodeToString(c.gcm.Seal(nonce, nonce, []byte(plaintext), nil)), nil
}

func (c *testAESCipher) Decrypt(encoded string) (string, error) {
	const prefix = "enc:"
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

// mailerService builds the mailer the tests enqueue through: any SMTP host
// makes it report configured, and no connection is dialed until a message is
// submitted.
func mailerService(cfg config.Config) (*mailer.Service, error) {
	m, err := mailer.New(cfg, nil)
	if err != nil {
		return nil, err
	}
	templates, err := mailer.NewTemplates(mailer.SenderFrom(cfg))
	if err != nil {
		return nil, err
	}
	return mailer.NewService(m, templates), nil
}

// seedUser writes an account row directly, so the tests drive the flow's own
// tables rather than another feature's procedures.
func seedUser(t *testing.T, pool *datastore.Postgres, username, email string) string {
	t.Helper()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto("public.users")
	ib.Cols("username", "email", "display_name")
	ib.Values(username, email, username)
	ib.SQL("RETURNING id")

	query, args := ib.Build()
	var id string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))
	return id
}

// wireOf renders an account's row identifier in the wire form the service
// procedures take, the shape the request carries.
func wireOf(t *testing.T, raw string) string {
	t.Helper()
	id, err := user.IDFromUUIDString(raw)
	require.NoError(t, err)
	return id.String()
}

// mustUUID parses a seeded row's identifier back into its UUID form.
func mustUUID(t *testing.T, raw string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(raw)
	require.NoError(t, err)
	return id
}

// countTokens reads how many code rows an account carries.
func countTokens(t *testing.T, pool *datastore.Postgres, userID string) int {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(tokenTable)
	sb.Where(
		sb.Equal("user_id", userID),
		sb.Equal("purpose", PurposeOneTimeAccess),
	)
	query, args := sb.Build()

	var count int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count
}

// pendingEmails reads how many one-time access messages are queued.
func pendingEmails(t *testing.T, client *queue.Client) int64 {
	t.Helper()

	pending, err := client.Pending(t.Context(), jobs.OneTimeAccessEmailName)
	require.NoError(t, err)
	return pending
}

func TestCreateTokenIssuesACodeTheExchangeAccepts(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, false, false)
	userID := seedUser(t, pool, "hermione", "hermione@example.com")

	code, expiresAt, err := service.CreateToken(t.Context(), wireOf(t, userID), 0)
	require.NoError(t, err)
	assert.Len(t, code, shortCodeLength, "a code without a window defaults to fifteen minutes, and that window's form is the short one")
	assert.True(t, expiresAt.After(time.Now()), "the expiry is in the future")

	// The exchange answers the token pair and names the session it opened.
	result, err := service.Exchange(t.Context(), code, "", audit.ClientInfo{})
	require.NoError(t, err)
	assert.Equal(t, "hermione", result.User.Username)
	assert.NotEmpty(t, result.AccessToken)
	assert.NotEmpty(t, result.RefreshToken)
	assert.NotEmpty(t, result.SessionID)

	// A longer window buys the longer form, on an account of its own: two
	// codes for one account cannot coexist, and the exchange below needs the
	// short one still standing.
	other := seedUser(t, pool, "langdon", "langdon@example.com")
	long, _, err := service.CreateToken(t.Context(), wireOf(t, other), 3600)
	require.NoError(t, err)
	assert.Len(t, long, longCodeLength)

	// The code is spent: a second exchange with the same value answers the
	// same refusal an unknown one does, and never a second session.
	_, err = service.Exchange(t.Context(), code, "", audit.ClientInfo{})
	assert.ErrorIs(t, err, ErrTokenInvalid)
	assert.Equal(t, 0, countTokens(t, pool, userID), "a spent code leaves no row behind")
}

func TestCreateTokenReplacesAnOlderCode(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, false, false)
	userID := seedUser(t, pool, "hermione", "hermione@example.com")

	first, _, err := service.CreateToken(t.Context(), wireOf(t, userID), 0)
	require.NoError(t, err)
	second, _, err := service.CreateToken(t.Context(), wireOf(t, userID), 0)
	require.NoError(t, err)

	assert.Equal(t, 1, countTokens(t, pool, userID),
		"an account carries one code at a time: the new one replaces the old")

	// The older code was replaced, not stacked, so it no longer works.
	_, err = service.Exchange(t.Context(), first, "", audit.ClientInfo{})
	assert.ErrorIs(t, err, ErrTokenInvalid)
	_, err = service.Exchange(t.Context(), second, "", audit.ClientInfo{})
	require.NoError(t, err)
}

// A re-issue replaces the pending row under the same id. The spend carries
// the hash the caller resolved, so a stale read cannot delete the replacement:
// the code it names is no longer the live one.
func TestDeleteTokenRefusesAStaleHash(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, false, false)
	userID := seedUser(t, pool, "hermione", "hermione@example.com")

	first, _, err := service.CreateToken(t.Context(), wireOf(t, userID), 0)
	require.NoError(t, err)
	stale, err := service.repo.FindTokenByHash(t.Context(), pool, crypto.HashHexToken(first))
	require.NoError(t, err)

	second, _, err := service.CreateToken(t.Context(), wireOf(t, userID), 0)
	require.NoError(t, err)

	spent, err := service.repo.DeleteToken(t.Context(), pool, stale.ID, crypto.HashHexToken(first), PurposeOneTimeAccess)
	require.NoError(t, err)
	assert.Zero(t, spent, "a stale hash removes nothing")
	assert.Equal(t, 1, countTokens(t, pool, userID), "the live code survives the stale delete")

	result, err := service.Exchange(t.Context(), second, "", audit.ClientInfo{})
	require.NoError(t, err)
	assert.Equal(t, "hermione", result.User.Username)
}

func TestExchangeRefusesAnExpiredCode(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, false, false)
	userID := seedUser(t, pool, "hermione", "hermione@example.com")

	code, _, err := service.CreateToken(t.Context(), wireOf(t, userID), 0)
	require.NoError(t, err)

	// The clock moves past the code's window: the code is refused, and the
	// row it occupied stays until the account's next code replaces it — the
	// refusal is inside the transaction that would have swept the row, and
	// the sweep is exactly what a rollback undoes. One row per account is
	// the bound the table carries, so nothing accumulates.
	service.now = func() time.Time { return time.Now().Add(time.Hour) }
	_, err = service.Exchange(t.Context(), code, "", audit.ClientInfo{})
	assert.ErrorIs(t, err, ErrTokenInvalid)
	assert.Equal(t, 1, countTokens(t, pool, userID))
}

func TestExchangeRefusesADeviceTokenThatDoesNotMatch(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, false, true)
	seedUser(t, pool, "hermione", "hermione@example.com")

	deviceToken, err := service.RequestEmail(t.Context(), "hermione@example.com", "")
	require.NoError(t, err)
	code := pendingOneTimeAccessTask(t, pool, service.queue).Token

	// The email request paired the code with a device token, so the exchange
	// demands it back exact. A wrong pair answers the mismatch, not the
	// unknown-code refusal: the holder must know to re-request rather than
	// retype.
	wrong, err := generateDeviceToken()
	require.NoError(t, err)
	_, err = service.Exchange(t.Context(), code, wrong, audit.ClientInfo{})
	assert.ErrorIs(t, err, ErrDeviceMismatch)

	// A mismatch leaves the code spendable: the mistake was the caller's,
	// not the code's spend.
	result, err := service.Exchange(t.Context(), code, deviceToken, audit.ClientInfo{})
	require.NoError(t, err)
	assert.Equal(t, "hermione", result.User.Username)
}

func TestRequestEmailAnswersTheSameForAnUnknownAddress(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, false, true)
	userID := seedUser(t, pool, "hermione", "hermione@example.com")

	known, err := service.RequestEmail(t.Context(), "hermione@example.com", "")
	require.NoError(t, err)
	assert.Len(t, known, deviceTokenLength)

	unknown, err := service.RequestEmail(t.Context(), "nobody@example.com", "")
	require.NoError(t, err, "an unknown address answers success, or the response is the enumeration")
	assert.Len(t, unknown, deviceTokenLength, "the answer carries a real device token either way")

	assert.Equal(t, int64(1), pendingEmails(t, service.queue),
		"only the known address queued a message")
	assert.Equal(t, 1, countTokens(t, pool, userID))
}

func TestRequestEmailPairsTheCodeWithADeviceToken(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, false, true)
	seedUser(t, pool, "hermione", "hermione@example.com")

	deviceToken, err := service.RequestEmail(t.Context(), "hermione@example.com", "/dashboard")
	require.NoError(t, err)

	// The code the task carries is the only place it exists; the queue's
	// pending task is where the test reads it from.
	task := pendingOneTimeAccessTask(t, pool, service.queue)
	assert.NotEmpty(t, task.Token)
	assert.Equal(t, "hermione@example.com", task.Email)

	result, err := service.Exchange(t.Context(), task.Token, deviceToken, audit.ClientInfo{})
	require.NoError(t, err)
	assert.Equal(t, "hermione", result.User.Username)

	// The code the email carried no longer works without the device token:
	// the row was consumed with the session it opened.
	_, err = service.Exchange(t.Context(), task.Token, deviceToken, audit.ClientInfo{})
	assert.ErrorIs(t, err, ErrTokenInvalid)
}

func TestRequestEmailRefusesADisabledPath(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	off := testService(t, pool, false, false)
	adminOnly := testService(t, pool, true, false)
	publicOnly := testService(t, pool, false, true)
	userID := seedUser(t, pool, "hermione", "hermione@example.com")

	_, err := off.RequestEmail(t.Context(), "hermione@example.com", "")
	assert.ErrorIs(t, err, ErrFeatureDisabled)
	err = off.RequestEmailAsAdmin(t.Context(), wireOf(t, userID), 0)
	assert.ErrorIs(t, err, ErrFeatureDisabled)

	err = adminOnly.RequestEmailAsAdmin(t.Context(), wireOf(t, userID), 0)
	assert.NoError(t, err, "the administrative path is open when its switch is")
	_, err = adminOnly.RequestEmail(t.Context(), "hermione@example.com", "")
	assert.ErrorIs(t, err, ErrFeatureDisabled, "the public path is closed while the administrative one is open")

	_, err = publicOnly.RequestEmail(t.Context(), "hermione@example.com", "")
	assert.NoError(t, err)
	err = publicOnly.RequestEmailAsAdmin(t.Context(), wireOf(t, userID), 0)
	assert.ErrorIs(t, err, ErrFeatureDisabled)
}

func TestRequestEmailAsAdminSendsWithoutExposingTheCode(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, true, false)
	userID := seedUser(t, pool, "hermione", "hermione@example.com")

	require.NoError(t, service.RequestEmailAsAdmin(t.Context(), wireOf(t, userID), 0))
	assert.Equal(t, int64(1), pendingEmails(t, service.queue))

	// The response carried no code, so the queued message is the only place
	// the value exists — and the row beside it holds a hash, not the value.
	code := pendingOneTimeAccessTask(t, pool, service.queue).Token
	row, err := service.repo.FindTokenByHash(t.Context(), pool, crypto.HashHexToken(code))
	require.NoError(t, err)
	assert.Nil(t, row.DeviceToken, "the administrative send pairs no device token")
}

func TestRequestEmailAsAdminRefusesAnUnknownAccount(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, true, false)

	err := service.RequestEmailAsAdmin(t.Context(), "00000000-0000-0000-0000-000000000000", 0)
	assert.ErrorIs(t, err, ErrUserNotFound)
}

func TestExchangeRefusesADisabledOrBannedAccount(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, false, false)

	disabled := seedUser(t, pool, "langdon", "langdon@example.com")
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update("public.users")
	ub.Set(ub.Assign("disabled", true))
	ub.Where(ub.Equal("id", disabled))
	query, args := ub.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)

	code, _, err := service.CreateToken(t.Context(), wireOf(t, disabled), 0)
	require.NoError(t, err)
	_, err = service.Exchange(t.Context(), code, "", audit.ClientInfo{})
	assert.ErrorIs(t, err, signin.ErrAccountDisabled)

	// The refusal rolled the spend back with the session it refused to open:
	// the code still works once the account is fit to sign in again.
	ub = sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update("public.users")
	ub.Set(ub.Assign("disabled", false))
	ub.Where(ub.Equal("id", disabled))
	query, args = ub.Build()
	_, err = pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)

	result, err := service.Exchange(t.Context(), code, "", audit.ClientInfo{})
	require.NoError(t, err)
	assert.Equal(t, "langdon", result.User.Username)
}

// pendingOneTimeAccessTask reads the queued message the last send left
// pending: the workers never start, so the task sits in the table and the
// code it carries is the only place the value exists.
func pendingOneTimeAccessTask(t *testing.T, pool *datastore.Postgres, client *queue.Client) jobs.OneTimeAccessEmailTask {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("task")
	sb.From("public.queue_tasks")
	sb.Where(sb.Equal("queue", jobs.OneTimeAccessEmailName))
	query, args := sb.Build()

	var payload []byte
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&payload))
	var task jobs.OneTimeAccessEmailTask
	require.NoError(t, json.Unmarshal(payload, &task))
	return task
}

// TestRequestEmailHoldsTheSendInsideTheCooldown pins the public path's
// answer to a retry: the same generic success, a fresh decoy device token,
// and no second message — while the code the first request sent goes on
// standing, pair intact.
func TestRequestEmailHoldsTheSendInsideTheCooldown(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, false, true)
	seedUser(t, pool, "hermione", "hermione@example.com")

	deviceToken, err := service.RequestEmail(t.Context(), "hermione@example.com", "")
	require.NoError(t, err)
	code := pendingOneTimeAccessTask(t, pool, service.queue).Token

	// The retry inside the window is the network's answer arriving twice.
	held, err := service.RequestEmail(t.Context(), "hermione@example.com", "")
	require.NoError(t, err, "a held send answers the same success a first one does")
	assert.Len(t, held, deviceTokenLength)
	assert.Equal(t, int64(1), pendingEmails(t, service.queue), "the held request enqueues nothing")
	assert.Equal(t, 1, countTokens(t, pool, readUserID(t, pool, "hermione@example.com")), "the held request replaces no code")

	// The first email's pair is untouched: the account holder signs in with
	// what already arrived.
	result, err := service.Exchange(t.Context(), code, deviceToken, audit.ClientInfo{})
	require.NoError(t, err)
	assert.Equal(t, "hermione", result.User.Username)

	// Past the window the send re-issues: the cooldown is a hold, not a
	// lifetime mute.
	service.now = func() time.Time { return time.Now().Add(2 * resendCooldown) }
	_, err = service.RequestEmail(t.Context(), "hermione@example.com", "")
	require.NoError(t, err)
	assert.Equal(t, int64(2), pendingEmails(t, service.queue))
}

// TestRequestEmailAsAdminRefusesInsideTheCooldown pins the administrative
// path: the caller knows the account is real, so the refusal is the answer,
// the way the admin reset trigger reports its own window.
func TestRequestEmailAsAdminRefusesInsideTheCooldown(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, true, false)
	seedUser(t, pool, "vittoria", "vittoria@example.com")
	wire := wireOf(t, readUserID(t, pool, "vittoria@example.com"))

	require.NoError(t, service.RequestEmailAsAdmin(t.Context(), wire, 0))
	assert.Equal(t, int64(1), pendingEmails(t, service.queue))

	err := service.RequestEmailAsAdmin(t.Context(), wire, 0)
	assert.ErrorIs(t, err, ErrResendTooSoon)
	assert.Equal(t, int64(1), pendingEmails(t, service.queue), "the refused request enqueues nothing")

	service.now = func() time.Time { return time.Now().Add(2 * resendCooldown) }
	require.NoError(t, service.RequestEmailAsAdmin(t.Context(), wire, 0))
	assert.Equal(t, int64(2), pendingEmails(t, service.queue))
}

// readUserID answers an account's row identifier by its address, the shape
// the seed helper returns and the administrative procedures take.
func readUserID(t *testing.T, pool *datastore.Postgres, email string) string {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From("public.users")
	sb.Where(sb.Equal("email", email))
	query, args := sb.Build()

	var id string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))
	return id
}

// TestExchangeOnAnMfaAccountMintsTheBridgeNotASession pins the fork the
// exchange runs: a code proves the mailbox, and an account keeping a
// confirmed authenticator answers the pending bridge — not a session. The
// bypass this test closes is the one an email link must never be: full
// access without the second factor.
func TestExchangeOnAnMfaAccountMintsTheBridgeNotASession(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service, mfa, _ := testServiceWithMfa(t, pool)
	userID := seedUser(t, pool, "hermione", "hermione@example.com")

	// The account keeps a password: the enrollment's account read joins the
	// password table, the way every issuer path does.
	_, err := pool.Exec(t.Context(),
		`INSERT INTO public.user_passwords (user_id, password_hash) VALUES ($1, $2)`,
		userID, "$scrypt$test")
	require.NoError(t, err)

	// The confirmed authenticator the account keeps.
	begun, err := mfa.BeginTotpEnrollment(t.Context(), mustUUID(t, userID), "Phone")
	require.NoError(t, err)
	now := time.Now()
	code, err := totp.GenerateCode(begun.Secret, now)
	require.NoError(t, err)
	_, err = mfa.ConfirmTotpEnrollment(t.Context(), mustUUID(t, userID), begun.TotpID, code)
	require.NoError(t, err)

	// The exchange answers the bridge, and no token field travels.
	token, _, err := service.CreateToken(t.Context(), wireOf(t, userID), 0)
	require.NoError(t, err)
	result, err := service.Exchange(t.Context(), token, "", audit.ClientInfo{})
	require.NoError(t, err)
	assert.True(t, result.MFARequired)
	assert.Empty(t, result.AccessToken)
	assert.Empty(t, result.RefreshToken)
	assert.NotEmpty(t, result.MFAPendingToken)

	// The bridge completes the sign-in: the same code the enrollment
	// confirmed opens the session, exactly as the password path would.
	next, err := totp.GenerateCode(begun.Secret, now.Add(31*time.Second))
	if err != nil {
		next, err = totp.GenerateCode(begun.Secret, now)
		require.NoError(t, err)
	}
	completed, err := mfa.CompleteSignIn(t.Context(), result.MFAPendingToken, next, nil, signin.SessionParams{})
	require.NoError(t, err)
	assert.NotEmpty(t, completed.AccessToken)
	assert.Equal(t, "hermione", completed.User.Username)
}
