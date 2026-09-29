package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"connectrpc.com/connect"

	identityv1 "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/jobs"
	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/testutils"
)

func migratedPool(t *testing.T) *datastore.Postgres {
	t.Helper()

	return testutils.MigratedPostgres(t, "verification_test")
}

// seedUser writes an account row directly, so the tests drive the flow's own
// tables rather than another feature's procedures.
func seedUser(t *testing.T, pool *datastore.Postgres, username, email string, verified bool) string {
	t.Helper()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto("public.users")
	if verified {
		ib.Cols("username", "email", "display_name", "email_verified_at")
		ib.Values(username, email, username, time.Now())
	} else {
		ib.Cols("username", "email", "display_name")
		ib.Values(username, email, username)
	}
	ib.SQL("RETURNING id")

	query, args := ib.Build()
	var id string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&id))
	return id
}

// seedToken writes the verification row a raw value hashes to.
func seedToken(t *testing.T, pool *datastore.Postgres, userID, raw string, expiresAt time.Time) {
	t.Helper()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(AuthTokenTable)
	ib.Cols("user_id", "token_hash", "purpose", "expires_at")
	ib.Values(userID, crypto.HashHexToken(raw), PurposeEmailVerification, expiresAt)

	query, args := ib.Build()
	_, err := pool.Exec(t.Context(), query, args...)
	require.NoError(t, err)
}

// testService builds the service over a queue whose processor is registered
// but whose workers never start: an enqueue lands in the table and stays
// pending, which is what the assertions read.
func testService(t *testing.T, pool *datastore.Postgres, configured bool) *Service {
	t.Helper()

	cfg := config.Default()
	cfg.Mailer.SMTPHost = ""
	if configured {
		// Any host makes the mailer report configured; no connection is
		// dialed until a message is submitted.
		cfg.Mailer.SMTPHost = "localhost"
	}
	mail, err := mailerService(cfg)
	require.NoError(t, err)

	client, err := queue.NewClient(queue.ClientConfig{
		Store:        pool,
		NumWorkers:   1,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)
	jobs.Register(client, time.Hour, nil, mail, pool, "http://localhost:3000", false, true, nil, nil, nil)

	return NewService(pool, mail, client, nil, "http://localhost:3000", nil)
}

func TestSendEmailRefusesTheStatesItCannotServe(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, true)

	_, err := pool.Exec(t.Context(), "SELECT 1")
	require.NoError(t, err)

	// An unknown account, a verified account, and an unconfigured mailer
	// each refuse with their own failure.
	err = service.SendEmail(t.Context(), "nobody")
	assert.ErrorIs(t, err, ErrUserNotFound)

	seedUser(t, pool, "verified", "patronus@example.com", true)
	err = service.SendEmail(t.Context(), "verified")
	assert.ErrorIs(t, err, ErrAlreadyVerified)

	unconfigured := testService(t, pool, false)
	seedUser(t, pool, "hermione", "hermione@example.com", false)
	err = unconfigured.SendEmail(t.Context(), "hermione")
	assert.ErrorIs(t, err, ErrMailUnavailable)
}

func TestSendEmailIssuesOneTokenPerAccount(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, true)
	seedUser(t, pool, "hermione", "hermione@example.com", false)

	require.NoError(t, service.SendEmail(t.Context(), "hermione"))

	// The table carries one row per account and purpose, and the queue
	// carries the message that will deliver it.
	pending, err := service.queue.Pending(t.Context(), jobs.EmailVerificationName)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pending)

	// A re-request inside the cooldown refuses. Past the window the same
	// request replaces the token: the row the first hash named no longer
	// exists, and the queue carries one more message, not a second row.
	err = service.SendEmail(t.Context(), "hermione")
	assert.ErrorIs(t, err, ErrResendTooSoon)

	service.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	require.NoError(t, service.SendEmail(t.Context(), "hermione"))

	pending, err = service.queue.Pending(t.Context(), jobs.EmailVerificationName)
	require.NoError(t, err)
	assert.Equal(t, int64(2), pending) // the first message and the re-issue

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(AuthTokenTable)
	sb.Where(sb.Equal("purpose", PurposeEmailVerification))
	query, args := sb.Build()
	var rows int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&rows))
	assert.Equal(t, 1, rows)
}

func TestVerifyEmailConsumesTheToken(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, true)
	userID := seedUser(t, pool, "hermione", "hermione@example.com", false)

	raw := "verification-token-value"
	seedToken(t, pool, userID, raw, time.Now().Add(tokenTTL))

	require.NoError(t, service.VerifyEmail(t.Context(), raw))

	// The account is verified and the token is spent: the row is gone, so
	// the same value verifies nothing the second time.
	var verifiedAt *time.Time
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("email_verified_at")
	sb.From("public.users")
	sb.Where(sb.Equal("username", "hermione"))
	query, args := sb.Build()
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&verifiedAt))
	require.NotNil(t, verifiedAt)

	err := service.VerifyEmail(t.Context(), raw)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestVerifyEmailRefusesAnUnknownAndAnExpiredToken(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	service := testService(t, pool, true)
	userID := seedUser(t, pool, "hermione", "hermione@example.com", false)

	err := service.VerifyEmail(t.Context(), "no-such-token")
	assert.ErrorIs(t, err, ErrInvalidToken)

	// The expiry is checked at consumption, not only at the insert the
	// table's check guards. The table refuses a backdated row, so the test
	// moves the service's clock past the window a fresh token closes with.
	seedToken(t, pool, userID, "expired-token-value", time.Now().Add(2*time.Second))
	service.now = func() time.Time { return time.Now().Add(time.Hour) }

	err = service.VerifyEmail(t.Context(), "expired-token-value")
	assert.ErrorIs(t, err, ErrInvalidToken)
}

// TestTheFlowEndToEnd walks the whole path against the real containers: the
// procedure issues the token and enqueues, the queue worker renders the
// template and submits, Mailpit receives the message, and the token the
// message's link carries verifies the account.
func TestTheFlowEndToEnd(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool := migratedPool(t)
	seedUser(t, pool, "hermione", "hermione@example.com", false)

	mailpit := testutils.StartMailpit(t.Context(), t)

	cfg := config.Default()
	cfg.Mailer.SMTPHost = host(mailpit.SMTPAddr)
	cfg.Mailer.SMTPPort = port(mailpit.SMTPAddr)
	cfg.Mailer.SMTPUsername = mailpit.Username
	cfg.Mailer.SMTPPassword = mailpit.Password
	mail, err := mailerService(cfg)
	require.NoError(t, err)

	client, err := queue.NewClient(queue.ClientConfig{
		Store:        pool,
		NumWorkers:   1,
		ReleaseAfter: time.Hour,
	})
	require.NoError(t, err)
	jobs.Register(client, time.Hour, nil, mail, pool, "http://localhost:3000", false, true, nil, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client.Start(ctx)
	t.Cleanup(func() { client.Shutdown(context.Background()) })

	service := NewService(pool, mail, client, nil, "http://localhost:3000", nil)
	require.NoError(t, service.SendEmail(t.Context(), "hermione"))

	link := waitForLink(t, mailpit)
	token := link[strings.Index(link, "token=")+len("token="):]
	require.NotEmpty(t, token)

	require.NoError(t, service.VerifyEmail(t.Context(), token))

	var verifiedAt *time.Time
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("email_verified_at")
	sb.From("public.users")
	sb.Where(sb.Equal("username", "hermione"))
	query, args := sb.Build()
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&verifiedAt))
	assert.NotNil(t, verifiedAt)
}

// waitForLink polls Mailpit until the verification message arrives and
// answers the link its body carries.
func waitForLink(t *testing.T, mailpit *testutils.Mailpit) string {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, mailpit.APIURL+"/api/v1/messages", nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		var listing struct {
			Messages []struct {
				ID      string `json:"ID"`
				Subject string `json:"Subject"`
			} `json:"messages"`
		}
		err = json.NewDecoder(resp.Body).Decode(&listing)
		resp.Body.Close()
		require.NoError(t, err)

		for _, message := range listing.Messages {
			if !strings.Contains(message.Subject, "Verify your email address") {
				continue
			}
			body := readMessageBody(t, mailpit, message.ID)
			if match := regexp.MustCompile(`https?://[^"'\s]+token=[^"'\s]+`).FindString(body); match != "" {
				return match
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the verification message never arrived")
	return ""
}

// readMessageBody reads one message's rendered text the Mailpit API serves.
func readMessageBody(t *testing.T, mailpit *testutils.Mailpit, id string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, mailpit.APIURL+"/api/v1/message/"+id, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var message struct {
		Text string `json:"Text"`
		HTML string `json:"HTML"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&message))
	return message.Text + message.HTML
}

// mailerService builds the service the flow sends through: the mailer over
// the configuration and the compiled templates beside it.
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

// host and port split the address Mailpit reports.
func host(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}

func port(addr string) int {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			var p int
			fmt.Sscanf(addr[i+1:], "%d", &p)
			return p
		}
	}
	return 0
}

func TestSendEmailRefusesACallerWithoutTheClaims(t *testing.T) {
	handler := &rpcHandler{service: nil} // the gate runs before the service

	_, err := handler.SendEmail(t.Context(), connect.NewRequest(&identityv1.SendVerificationEmailRequest{}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestMapErrorCarriesTheConnectCodes(t *testing.T) {
	cases := []struct {
		err  error
		code connect.Code
	}{
		{ErrUserNotFound, connect.CodeNotFound},
		{ErrAlreadyVerified, connect.CodeFailedPrecondition},
		{ErrMailUnavailable, connect.CodeUnavailable},
		{ErrInvalidToken, connect.CodePermissionDenied},
		{ErrResendTooSoon, connect.CodeResourceExhausted},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.code, connect.CodeOf(mapError(tc.err)), "%v", tc.err)
	}
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(mapError(errors.New("boom"))))
}
