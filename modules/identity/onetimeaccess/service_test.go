package onetimeaccess

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"log/slog"

	"github.com/riipandi/tango/database"
	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/jobs"
	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/modules/identity/jwks"
	"github.com/riipandi/tango/modules/identity/signin"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/testutils"
)

// testSecretHex is the HMAC secret the tests sign with: any 32-byte hex
// value, the same one the sign-in tests use.
const testSecretHex = "0123456789abcdeffedcba98765432100123456789abcdeffedcba9876543210"

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
		ApplicationName: "onetimeaccess_test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })
	return pool
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Auth.PrivateKey = ""
	cfg.Auth.PublicKey = ""
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
		ReleaseAfter: 10 * time.Minute,
	})
	require.NoError(t, err)
	jobs.Register(client, time.Hour, nil, mail, pool, "http://localhost:3000", false, true)

	issuer := signin.NewService(testConfig(), pool, signin.NewRepository(pool),
		jwks.NewService(testConfig(), nil, nil), nil, nil)
	return NewService(cfg, pool, issuer, audit.NewRecorder(slog.New(slog.DiscardHandler)), mail, client, nil)
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
	row, err := service.repo.FindTokenByHash(t.Context(), pool, codeSHA256(code))
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
