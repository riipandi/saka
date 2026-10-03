package mailer_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"encoding/json/v2"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/pkg/testutils"
)

// startMailer points the configuration at the shared Mailpit container and
// builds the mailer against it. The container requires a Docker daemon, so a run
// without one skips rather than fails.
func startMailer(t *testing.T) (*mailer.Service, *testutils.Mailpit) {
	t.Helper()

	server := testutils.StartMailpit(t.Context(), t)
	cfg := mailpitConfig(t, server)

	client, err := mailer.New(cfg, nil)
	require.NoError(t, err)
	templates, err := mailer.NewTemplates(mailer.SenderFrom(cfg))
	require.NoError(t, err)
	return mailer.NewService(client, templates), server
}

// mailpitHostPort splits the container's SMTP address into the two fields the
// configuration takes.
func mailpitHostPort(t *testing.T, server *testutils.Mailpit) (string, int) {
	t.Helper()

	host, portText, err := net.SplitHostPort(server.SMTPAddr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return host, port
}

// mailpitConfig is the configuration that reaches the container.
func mailpitConfig(t *testing.T, server *testutils.Mailpit) config.Config {
	t.Helper()

	cfg := config.Default()
	cfg.Mailer.SMTPHost, cfg.Mailer.SMTPPort = mailpitHostPort(t, server)
	cfg.Mailer.SMTPUsername = server.Username
	cfg.Mailer.SMTPPassword = server.Password
	cfg.Mailer.FromEmail = "no-reply@saka.test"
	cfg.Mailer.FromName = "Hogwarts Express"
	return cfg
}

func TestSendDeliversATemplatedMessage(t *testing.T) {
	// The whole path: render a template, build the MIME message, authenticate,
	// and hand it to a real SMTP server.
	service, server := startMailer(t)

	recipient := "neveu@saka.test"
	subject := uniqueSubject("mailer smoke")
	require.NoError(t, service.Send(t.Context(), mailer.Request{
		To:       []string{recipient},
		Subject:  subject,
		Template: mailer.TemplatePasswordReset,
		View: mailer.View{Data: mailer.PasswordResetData{
			Email:     recipient,
			ResetCode: "expecto-patronum",
		}},
	}))

	message := awaitMessage(t, server, subject)
	assert.Equal(t, subject, message.Subject)
	assert.Contains(t, message.Visible(), recipient)

	// The HTML rendering is what the recipient sees, so the link has to arrive
	// intact and the identity has to be there.
	detail := fetchMessage(t, server, message.ID)
	assert.Contains(t, detail.HTML, "expecto-patronum")
	assert.Contains(t, detail.HTML, "Saka")
	assert.NotContains(t, detail.HTML, "{{")
}

func TestSendReachesBlindRecipientsWithoutShowingThem(t *testing.T) {
	service, server := startMailer(t)

	subject := uniqueSubject("mailer bcc")
	require.NoError(t, service.Send(t.Context(), mailer.Request{
		To:       []string{"primary@saka.test"},
		Bcc:      []string{"blind@saka.test"},
		Subject:  subject,
		Template: mailer.TemplateTestEmail,
		View:     mailer.View{Email: "primary@saka.test"},
	}))

	message := awaitMessage(t, server, subject)
	assert.ElementsMatch(t, []string{"primary@saka.test", "blind@saka.test"}, message.Delivered(),
		"a blind recipient is still delivered to")

	// Mailpit parses the message the way a receiving client does, so the
	// headers it reports are the headers that were sent.
	assert.Equal(t, []string{"primary@saka.test"}, message.Visible(),
		"a blind recipient must not appear in the headers")

	detail := fetchMessage(t, server, message.ID)
	assert.NotContains(t, detail.Text, "blind@saka.test",
		"a blind recipient must not appear in the message")
}

func TestSendReportsRejectedCredentials(t *testing.T) {
	// The class a caller matches has to survive the whole session, not just the
	// classification function.
	_, server := startMailer(t)

	cfg := mailpitConfig(t, server)
	cfg.Mailer.SMTPPassword = "wrong-password"
	client, err := mailer.New(cfg, nil)
	require.NoError(t, err)
	templates, err := mailer.NewTemplates(mailer.SenderFrom(cfg))
	require.NoError(t, err)

	err = mailer.NewService(client, templates).Send(t.Context(), mailer.Request{
		To:       []string{"neveu@saka.test"},
		Subject:  "should not arrive",
		Template: mailer.TemplateTestEmail,
		View:     mailer.View{Email: "neveu@saka.test"},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, mailer.ErrAuth)
}

func TestSendAllowsPlaintextCredentialsToALoopbackServer(t *testing.T) {
	// The exemption that keeps a local development server usable without a
	// certificate. Mailpit without TLS is reached over loopback, so no opt-in
	// is needed.
	server := testutils.StartMailpit(t.Context(), t)

	cfg := config.Default()
	cfg.Mailer.SMTPHost, cfg.Mailer.SMTPPort = mailpitHostPort(t, server)
	cfg.Mailer.SMTPUsername = server.Username
	cfg.Mailer.SMTPPassword = server.Password

	client, err := mailer.New(cfg, nil)
	require.NoError(t, err)

	subject := uniqueSubject("mailer loopback plaintext")
	require.NoError(t, client.Send(t.Context(), mailer.Message{
		To:      []string{"neveu@saka.test"},
		Subject: subject,
		Text:    "Hello",
	}))
	assert.NotEmpty(t, awaitMessage(t, server, subject))
}

// uniqueSubject makes a subject this run can be found by. The container is
// shared with every other test in the binary, so a search names the subject
// rather than looking for an empty mailbox.
func uniqueSubject(prefix string) string {
	return prefix + " " + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// mailpitMessage is one message as Mailpit's search API reports it. To holds the
// visible recipients and Bcc the blind ones, which is what makes the delivery of
// a blind recipient checkable.
type mailpitMessage struct {
	ID      string `json:"ID"`
	Subject string `json:"Subject"`
	To      []struct {
		Address string `json:"Address"`
	} `json:"To"`
	Bcc []struct {
		Address string `json:"Address"`
	} `json:"Bcc"`
}

// Delivered lists every address the message was delivered to.
func (m mailpitMessage) Delivered() []string {
	out := make([]string, 0, len(m.To)+len(m.Bcc))
	for _, addr := range append(m.To, m.Bcc...) {
		out = append(out, addr.Address)
	}
	return out
}

// Visible lists the recipients a reader sees in the headers.
func (m mailpitMessage) Visible() []string {
	out := make([]string, 0, len(m.To))
	for _, addr := range m.To {
		out = append(out, addr.Address)
	}
	return out
}

// mailpitDetail is one message's full body, which is where a rendered template
// and a header can be checked together.
type mailpitDetail struct {
	HTML string `json:"HTML"`
	Text string `json:"Text"`
}

// awaitMessage polls Mailpit until the message a run just sent appears.
func awaitMessage(t *testing.T, server *testutils.Mailpit, subject string) mailpitMessage {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if messages := searchMessages(t, server, subject); len(messages) > 0 {
			return messages[0]
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no message with subject %q arrived within the deadline", subject)
	return mailpitMessage{}
}

// searchMessages asks Mailpit for the messages matching a subject.
func searchMessages(t *testing.T, server *testutils.Mailpit, subject string) []mailpitMessage {
	t.Helper()

	query := url.Values{"query": {"subject:" + strconv.Quote(subject)}}
	response := getJSON[struct {
		Messages []mailpitMessage `json:"messages"`
	}](t, server.APIURL+"/api/v1/search?"+query.Encode())
	return response.Messages
}

// fetchMessage reads one message's body.
func fetchMessage(t *testing.T, server *testutils.Mailpit, id string) mailpitDetail {
	t.Helper()
	return getJSON[mailpitDetail](t, server.APIURL+"/api/v1/message/"+id)
}

// getJSON reads one JSON document from the container's API.
func getJSON[T any](t *testing.T, target string) T {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	var out T
	require.NoError(t, json.Unmarshal(body, &out), "body: %s", strings.TrimSpace(string(body)))
	return out
}
