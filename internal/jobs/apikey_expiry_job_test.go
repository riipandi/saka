package jobs

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/datastore"
	fwmailer "github.com/riipandi/saka/framework/mailer"
	"github.com/riipandi/saka/framework/queue"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/modules/apikey"
	"github.com/riipandi/saka/pkg/testutils"

	"uuid"
)

// expiryPool opens a migrated database and a queue client whose processors
// are wired the way a serve run wires them, with the reminder switch the test
// asks for.
func expiryPool(t *testing.T, enabled bool, mail *fwmailer.Service) (*datastorePostgres, *queue.Client) {
	t.Helper()

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)
	pool, client := migratedClient(t, dsn)
	Register(client, time.Hour, nil, mail, pool, "", enabled, true, nil, nil, nil, nil, nil, nil, nil)
	return pool, client
}

// TestTheExpiryScanRemindsTheWindowOnce runs the reminder end to end: a scan
// enqueues one reminder for the key inside the window, sends it through the
// real mailer, and marks it — so the next pass finds nothing and sends
// nothing again.
func TestTheExpiryScanRemindsTheWindowOnce(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	mail, mailpitServer := startExpiryMailer(t)
	pool, client := expiryPool(t, true, mail)

	seedKeyOwner(t, pool)
	service := apikey.NewService(pool, nil, nil)
	_, err := service.Create(t.Context(), mustOwnerID(t, pool, "hermione"), apikey.CreateParams{
		Name:      "soon-key",
		ExpiresAt: time.Now().Add(5 * 24 * time.Hour),
	})
	require.NoError(t, err)
	_, err = service.Create(t.Context(), mustOwnerID(t, pool, "hermione"), apikey.CreateParams{
		Name:      "far-key",
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	})
	require.NoError(t, err)

	// One pass, dispatched the way a serve run dispatches it. The task is
	// added with its real schedule, so the successor it queues sits a day
	// out and the pending count settles at one.
	_, err = client.Add(APIKeyExpiryScanTask{}).Save()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	defer cancel()
	client.Start(ctx)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer stopCancel()
		client.Stop(stopCtx)
	})

	// The message arrives through the queue, rendered by the template the
	// reminder names.
	message := awaitExpiryMessage(t, mailpitServer, "Your API key expires soon")
	require.NotEmpty(t, message.To)
	assert.Equal(t, "hermione@example.com", message.To[0].Address)

	// The scan's mark is what keeps the next pass from repeating the
	// reminder: the in-window key is stamped, the out-of-window one is not,
	// and the record of the send is in the log.
	require.Eventually(t, func() bool {
		return expiryMarkCount(t, pool, "soon-key") == 1
	}, 20*time.Second, 25*time.Millisecond, "the scan must mark the key it reminded")
	assert.Equal(t, int64(0), expiryMarkCount(t, pool, "far-key"))
	assert.Equal(t, 1, expiryAuditCount(t, pool))
}

// TestTheExpiryScanHonoursTheSwitch keeps a deployment that asked for no
// reminders from receiving any: the pass runs, finds nothing asked of it, and
// reschedules.
func TestTheExpiryScanHonoursTheSwitch(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	pool, client := expiryPool(t, false, nil)

	seedKeyOwner(t, pool)
	service := apikey.NewService(pool, nil, nil)
	_, err := service.Create(t.Context(), mustOwnerID(t, pool, "hermione"), apikey.CreateParams{
		Name:      "soon-key",
		ExpiresAt: time.Now().Add(5 * 24 * time.Hour),
	})
	require.NoError(t, err)

	_, err = client.Add(APIKeyExpiryScanTask{}).Save()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	defer cancel()
	client.Start(ctx)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer stopCancel()
		client.Stop(stopCtx)
	})

	// The successor is the signal that the pass ran and did nothing.
	require.Eventually(t, func() bool {
		var waitUntil *time.Time
		err := pool.QueryRow(t.Context(),
			`SELECT wait_until FROM public.queue_tasks WHERE queue = $1`, APIKeyExpiryScanName).Scan(&waitUntil)
		return err == nil && waitUntil != nil && waitUntil.After(time.Now().Add(time.Hour))
	}, 20*time.Second, 25*time.Millisecond, "the pass must reschedule without reminding")
	assert.Equal(t, 0, expiryAuditCount(t, pool))
}

// TestTheExpiryScanQueuesNoSuccessorWhenTheScanFails pins the schedule's
// integrity: a scan that fails leaves no successor, so the queue's retry of
// the same attempt is the only run — a successor committed before the scan
// would let three attempts leave three daily chains, and two chains could
// both remind a key before either marked it.
func TestTheExpiryScanQueuesNoSuccessorWhenTheScanFails(t *testing.T) {
	testutils.SkipWithoutDocker(t)

	dsn := testutils.StartPostgres(t.Context(), t).NewDatabase(t)
	pool, client := migratedClient(t, dsn)
	Register(client, time.Hour, nil, nil, pool, "", true, true, nil, nil, nil, nil, nil, nil, nil)

	// A second pool on the same database, closed before the call: the scan's
	// list is refused, while the client's own pool stays live for the
	// assertion.
	dead, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{
		DSN:             dsn,
		ApplicationName: "expiry_dead",
	})
	require.NoError(t, err)
	dead.Shutdown(context.Background())

	scanErr := apiKeyExpiryScanProcessor(t.Context(), APIKeyExpiryScanTask{}, dead, client, true,
		&fwmailer.Service{})
	require.Error(t, scanErr, "a failed scan must report the failure")

	// No successor: the pending row is what a committed successor would be,
	// and there is none.
	pending, countErr := client.Pending(t.Context(), APIKeyExpiryScanName)
	require.NoError(t, countErr)
	assert.Zero(t, pending, "a failed scan must not commit a successor")
}

// The helpers the expiry tests share with the fixture they run against.

// datastorePostgres aliases the pool type the migrated client answers, so the
// helper signatures stay readable.
type datastorePostgres = datastore.Postgres

// seedKeyOwner inserts the account a fixture key belongs to.
func seedKeyOwner(t *testing.T, pool *datastore.Postgres) {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.users (username, email, first_name, last_name, display_name)
		VALUES ('hermione', 'hermione@example.com', 'Hermione', 'Granger', 'Hermione Granger')`)
	require.NoError(t, err)
}

// mustOwnerID answers the fixture account's identifier.
func mustOwnerID(t *testing.T, pool *datastore.Postgres, username string) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT id FROM public.users WHERE username = $1`, username).Scan(&id))
	return id
}

// startExpiryMailer builds the mailer over the shared Mailpit container.
func startExpiryMailer(t *testing.T) (*fwmailer.Service, *testutils.Mailpit) {
	t.Helper()

	server := testutils.StartMailpit(t.Context(), t)
	cfg := config.Default()
	host, port, portErr := net.SplitHostPort(server.SMTPAddr)
	require.NoError(t, portErr)
	cfg.Mailer.SMTPHost = host
	smtpPort, convErr := strconv.Atoi(port)
	require.NoError(t, convErr)
	cfg.Mailer.SMTPPort = smtpPort
	cfg.Mailer.SMTPUsername = server.Username
	cfg.Mailer.SMTPPassword = server.Password
	cfg.Mailer.FromEmail = "no-reply@hogwarts.test"

	client, err := fwmailer.New(cfg.MailerOptions(), nil)
	require.NoError(t, err)
	templates, err := mailer.NewTemplates(mailer.SenderFrom(cfg))
	require.NoError(t, err)
	return fwmailer.NewService(client, templates), server
}

// expiryMarkCount reads how many times a key carries the reminder stamp.
func expiryMarkCount(t *testing.T, pool *datastore.Postgres, name string) int64 {
	t.Helper()

	var count int64
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.api_keys WHERE name = $1 AND expiration_email_sent_at IS NOT NULL`,
		name).Scan(&count))
	return count
}

// expiryAuditCount reads how many records the reminder's event holds.
func expiryAuditCount(t *testing.T, pool *datastore.Postgres) int {
	t.Helper()

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM public.audit_logs WHERE event = $1`,
		audit.EventAPIKeyExpiryEmailSent).Scan(&count))
	return count
}

// mailpitMessage is one message Mailpit answers a search with.
type mailpitMessage struct {
	ID string `json:"ID"`
	To []struct {
		Address string `json:"Address"`
	} `json:"To"`
}

// awaitExpiryMessage polls Mailpit until the message the scan sent appears.
func awaitExpiryMessage(t *testing.T, server *testutils.Mailpit, subject string) mailpitMessage {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		query := url.Values{"query": {"subject:" + strconv.Quote(subject)}}
		response, err := http.Get(server.APIURL + "/api/v1/search?" + query.Encode()) // nolint
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			require.NoError(t, readErr)
			var out struct {
				Messages []mailpitMessage `json:"messages"`
			}
			require.NoError(t, json.Unmarshal(body, &out))
			if len(out.Messages) > 0 {
				return out.Messages[0]
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no message with subject %q arrived within the deadline", subject)
	return mailpitMessage{}
}
