package appconfig

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/pkg/testutils"
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
	ib.InsertInto(entity.TableUsers)
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

	return NewService(pool, audit.NewRecorder(slog.New(slog.DiscardHandler)), mailer.NewService(mail, templates), nil)
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

	return NewService(pool, audit.NewRecorder(slog.New(slog.DiscardHandler)), mailer.NewService(mail, templates), nil)
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
