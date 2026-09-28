package appconfig

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/pkg/testutils"
)

func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "appconfig_test")
}

// seedUser writes an account row directly, so the tests drive the flow's own
// tables rather than another feature's procedures.
func seedUser(t *testing.T, pool *datastore.Postgres, username, email string) uuid.UUID {
	t.Helper()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto("public.users")
	ib.Cols("username", "email", "display_name")
	ib.Values(username, email, username)
	ib.SQL("RETURNING id")

	query, args := ib.Build()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))
	return id
}

// testService builds the service over a mailer pointed at the running
// Mailpit: a synchronous send needs a server that answers, because the
// SMTP attempt is the procedure's whole outcome.
func testService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()

	mailpit := testutils.StartMailpit(t.Context(), t)

	cfg := config.Default()
	cfg.Mailer.SMTPHost = host(mailpit.SMTPAddr)
	cfg.Mailer.SMTPPort = port(mailpit.SMTPAddr)
	cfg.Mailer.SMTPUsername = mailpit.Username
	cfg.Mailer.SMTPPassword = mailpit.Password
	mail, err := mailer.New(cfg, nil)
	require.NoError(t, err)
	templates, err := mailer.NewTemplates(mailer.SenderFrom(cfg))
	require.NoError(t, err)

	return NewService(cfg, pool, audit.NewRecorder(slog.New(slog.DiscardHandler)), mailer.NewService(mail, templates), nil)
}

// unconfiguredService builds the service the way a deployment without an
// SMTP host is built: the mailer resolves, it just names no server.
func unconfiguredService(t *testing.T, pool *datastore.Postgres) *Service {
	t.Helper()

	cfg := config.Default()
	cfg.Mailer.SMTPHost = ""
	mail, err := mailer.New(cfg, nil)
	require.NoError(t, err)
	templates, err := mailer.NewTemplates(mailer.SenderFrom(cfg))
	require.NoError(t, err)

	return NewService(cfg, pool, audit.NewRecorder(slog.New(slog.DiscardHandler)), mailer.NewService(mail, templates), nil)
}

func host(addr string) string {
	only, _, _ := strings.Cut(addr, ":")
	return only
}

func port(addr string) int {
	_, only, _ := strings.Cut(addr, ":")
	port, err := strconv.Atoi(only)
	if err != nil {
		return 0
	}
	return port
}

// messageList is the part of the Mailpit API's index the assertions read.
type messageList struct {
	Total    int `json:"total"`
	Messages []struct {
		To []struct {
			Address string `json:"Address"`
		} `json:"To"`
		Subject string `json:"Subject"`
	} `json:"messages"`
}

// mailpitMessages reads the shared container's index. The container is
// shared across the test binary, so the count assertions read the delta
// a test's own send causes rather than an absolute number.
func mailpitMessages(ctx context.Context, apiURL string) messageList {
	var list messageList
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/api/v1/messages", nil)
	if err != nil {
		return list
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return list
	}
	defer resp.Body.Close()
	_ = json.NewDecoder(resp.Body).Decode(&list)
	return list
}

// subjectAt reports whether the index holds a message with the test
// subject, addressed exactly as the expectation names.
func subjectAt(t *testing.T, list messageList, to ...string) bool {
	t.Helper()

	for _, message := range list.Messages {
		if message.Subject != testEmailSubject {
			continue
		}
		addresses := make([]string, 0, len(message.To))
		for _, recipient := range message.To {
			addresses = append(addresses, recipient.Address)
		}
		return assert.Equal(t, to, addresses)
	}
	return false
}

func TestTestEmailSendsToTheCallerSAddressOnRecord(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	callerID := seedUser(t, pool, "langdon", "langdon@example.com")

	mailpit := testutils.StartMailpit(t.Context(), t)
	before := mailpitMessages(t.Context(), mailpit.APIURL)
	require.NoError(t, service.SendTestEmail(t.Context(), callerID, ""))

	list := mailpitMessages(t.Context(), mailpit.APIURL)
	assert.Equal(t, before.Total+1, list.Total)
	assert.True(t, subjectAt(t, list, "langdon@example.com"),
		"the test message reached the caller's address on record")
}

func TestTestEmailFollowsTheRedirectionTheRequestNames(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	callerID := seedUser(t, pool, "neveu", "neveu@example.com")

	mailpit := testutils.StartMailpit(t.Context(), t)
	before := mailpitMessages(t.Context(), mailpit.APIURL)
	require.NoError(t, service.SendTestEmail(t.Context(), callerID, "vetra@example.com"))

	list := mailpitMessages(t.Context(), mailpit.APIURL)
	assert.Equal(t, before.Total+1, list.Total)
	assert.True(t, subjectAt(t, list, "vetra@example.com"),
		"the test message went where the request redirected it")
}

func TestTestEmailRefusesAMailerThatNamesNoServer(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := unconfiguredService(t, pool)
	callerID := seedUser(t, pool, "vetra", "vetra@example.com")

	err := service.SendTestEmail(t.Context(), callerID, "")
	assert.ErrorIs(t, err, ErrMailUnavailable)
}

func TestTestEmailRefusesACallerTheDatabaseDoesNotKnow(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	live := seedUser(t, pool, "hogwarts", "hogwarts@example.com")
	require.NoError(t, service.SendTestEmail(t.Context(), live, ""))

	deleted := seedUser(t, pool, "patronus", "patronus@example.com")
	_, err := pool.Exec(t.Context(), "DELETE FROM public.users WHERE id = $1", deleted)
	require.NoError(t, err)

	err = service.SendTestEmail(t.Context(), deleted, "")
	assert.ErrorIs(t, err, ErrUnknownAccount)
}

func TestTestEmailWritesTheAuditRecordTheSendDeserves(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool)
	callerID := seedUser(t, pool, "granger", "granger@example.com")

	require.NoError(t, service.SendTestEmail(t.Context(), callerID, "gryffindor@example.com"))

	row := pool.QueryRow(t.Context(), `
		SELECT COALESCE(user_id::text, ''), payload::text
		FROM public.audit_logs WHERE event = $1`, audit.EventTestEmailSent)
	var userID, payload string
	require.NoError(t, row.Scan(&userID, &payload))
	assert.Equal(t, callerID.String(), userID)
	assert.Contains(t, payload, "gryffindor@example.com")
	assert.Contains(t, payload, `"redirected"`)
}

// TestGetPublicPublishesOnlyTheBootstrapFacts pins the public subset: the
// facts a login screen shows and nothing else, so a key added to the
// configuration later stays private until the mapping names it.
func TestGetPublicPublishesOnlyTheBootstrapFacts(t *testing.T) {
	cfg := config.Default()
	cfg.App.Mode = config.ModeProduction
	cfg.App.BaseURL = "https://id.example.com"
	cfg.App.AssetsURL = "https://cdn.example.com"
	cfg.Auth.OneTimeAccessEmailAsAdminEnabled = true
	cfg.Mailer.Notifications.AnnouncementEmailEnabled = true

	public := NewService(cfg, nil, nil, nil, nil).GetPublic()

	assert.Equal(t, "production", public.GetApp().GetMode())
	assert.Equal(t, "https://id.example.com", public.GetApp().GetBaseUrl())
	assert.Equal(t, "https://cdn.example.com", public.GetApp().GetAssetsUrl())
	assert.True(t, public.GetAuth().GetOneTimeAccessEmailAsAdminEnabled())
	assert.False(t, public.GetAuth().GetOneTimeAccessEmailAsUnauthenticatedEnabled())
	assert.True(t, public.GetMailer().GetAnnouncementEmailEnabled())
}

// TestGetAllPublishesNoSecret plants every secret value in the configuration
// and reads the administrator's answer back as JSON: if a secret reached a
// published field, the planted value would show up here.
func TestGetAllPublishesNoSecret(t *testing.T) {
	cfg := config.Default()
	secrets := map[string]*string{
		"app.secret_key":               &cfg.App.SecretKey,
		"auth.private_key":             &cfg.Auth.PrivateKey,
		"auth.public_key":              &cfg.Auth.PublicKey,
		"auth.secret_key":              &cfg.Auth.SecretKey,
		"database.url":                 &cfg.Database.URL,
		"kvstore.url":                  &cfg.KVStore.URL,
		"mailer.smtp_password":         &cfg.Mailer.SMTPPassword,
		"storage.s3.access_key_secret": &cfg.Storage.S3.AccessKeySecret,
	}
	for _, v := range secrets {
		*v = "planted-hogwarts-secret"
	}
	cfg.OTEL.Headers = map[string]string{"authorization": "planted-hogwarts-secret"}

	document, err := protojson.Marshal(NewService(cfg, nil, nil, nil, nil).GetAll())
	require.NoError(t, err)
	assert.NotContains(t, string(document), "planted-hogwarts-secret",
		"a published field carries a secret; the mapping must not name it")
}

// TestGetAllCarriesTheRunningConfiguration pins representative fields across
// the sections, so a mapping dropped in a refactor fails here.
func TestGetAllCarriesTheRunningConfiguration(t *testing.T) {
	cfg := config.Default()
	cfg.App.Mode = config.ModeStaging
	cfg.Auth.AccessTTL = 15 * time.Minute
	cfg.Server.Port = 4080
	cfg.Server.CORS.AllowedOrigins = []string{"https://app.example.com"}
	cfg.Storage.S3.Region = "ap-southeast-1"

	full := NewService(cfg, nil, nil, nil, nil).GetAll()

	assert.Equal(t, "staging", full.GetApp().GetMode())
	assert.Equal(t, durationpb.New(15*time.Minute), full.GetAuth().GetAccessTtl())
	assert.Equal(t, int64(4080), full.GetServer().GetPort())
	assert.Equal(t, []string{"https://app.example.com"}, full.GetServer().GetCors().GetAllowedOrigins())
	assert.Equal(t, "ap-southeast-1", full.GetStorage().GetS3().GetRegion())
}
