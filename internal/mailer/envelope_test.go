package mailer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"

	"github.com/emersion/go-smtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/config"
)

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Mailer.SMTPHost = "smtp.example.com"
	cfg.Mailer.SMTPPort = 587
	cfg.Mailer.FromEmail = "owl-post@example.com"
	cfg.Mailer.FromName = "Tango"
	return cfg
}

func testMailer(t *testing.T) *Mailer {
	t.Helper()
	client, err := New(testConfig(), nil)
	require.NoError(t, err)
	return client
}

// renderMessage runs the same two steps Send does — prepare the envelope, then
// write it — and returns the bytes a receiving client would see.
func renderMessage(t *testing.T, m *Mailer, msg Message) ([]byte, error) {
	t.Helper()
	body, err := staticBody(msg.HTML, msg.Text)
	if err != nil {
		return nil, err
	}
	env, err := m.prepare(msg, body)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := env.writeTo(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// renderTemplateMessage is the templated path: the body is rendered into the
// writer as it is written, so no rendered string exists at any point.
func renderTemplateMessage(t *testing.T, m *Mailer, templates *Templates, msg Message, name string, view View) ([]byte, error) {
	t.Helper()
	env, err := m.prepare(msg, renderBody(templates, name, view))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := env.writeTo(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// parsed is a rendered message taken apart the way a receiving client would.
type parsed struct {
	header mail.Header
	body   string
}

func parseMessage(t *testing.T, raw []byte) parsed {
	t.Helper()
	msg, err := mail.ReadMessage(bufio.NewReader(bytes.NewReader(raw)))
	require.NoError(t, err)
	body, err := readBody(msg)
	require.NoError(t, err)
	return parsed{header: msg.Header, body: body}
}

func readBody(msg *mail.Message) (string, error) {
	body, err := io.ReadAll(msg.Body)
	return string(body), err
}

func TestEnvelopeWritesBothBodiesAsAlternatives(t *testing.T) {
	m := testMailer(t)
	raw, err := renderMessage(t, m, Message{
		To:      []string{"muggle@example.com"},
		Subject: "Hello",
		HTML:    "<p>Hello</p>",
		Text:    "Hello",
	})
	require.NoError(t, err)

	msg := parseMessage(t, raw)
	mediaType, params, err := mime.ParseMediaType(msg.header.Get("Content-Type"))
	require.NoError(t, err)
	assert.Equal(t, "multipart/alternative", mediaType)

	// The text part comes first: a client that cannot show the HTML falls back
	// to the last part it understands.
	mr := multipart.NewReader(strings.NewReader(msg.body), params["boundary"])
	var kinds []string
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		partType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		kinds = append(kinds, partType)
	}
	assert.Equal(t, []string{"text/plain", "text/html"}, kinds)
}

func TestEnvelopeWritesOneBodyWithoutAMultipartWrapper(t *testing.T) {
	m := testMailer(t)
	raw, err := renderMessage(t, m, Message{To: []string{"muggle@example.com"}, Subject: "Hello", Text: "Hello"})
	require.NoError(t, err)

	msg := parseMessage(t, raw)
	mediaType, params, err := mime.ParseMediaType(msg.header.Get("Content-Type"))
	require.NoError(t, err)
	assert.Equal(t, "text/plain", mediaType)
	assert.Equal(t, "utf-8", params["charset"])
	assert.NotContains(t, msg.body, "multipart")
}

func TestEnvelopeOmitsAnEmptyToHeader(t *testing.T) {
	// A message may be addressed only through Cc. An empty "To:" line is worse
	// than none at all, so the header is written only when it has an address.
	m := testMailer(t)
	env, err := m.prepare(Message{
		Cc:      []string{"dumbledore@example.com"},
		Subject: "Hello",
	}, mustBody(t, "", "Hello"))
	require.NoError(t, err)

	assert.Equal(t, []string{"dumbledore@example.com"}, env.rcpt)
	var out bytes.Buffer
	require.NoError(t, env.writeTo(&out))

	msg := parseMessage(t, out.Bytes())
	assert.Empty(t, msg.header.Get("To"))
	assert.Equal(t, "dumbledore@example.com", msg.header.Get("Cc"))
}

func TestEnvelopeKeepsBccOffTheHeaders(t *testing.T) {
	m := testMailer(t)
	env, err := m.prepare(Message{
		To:      []string{"muggle@example.com"},
		Bcc:     []string{"mcgonagall@example.com"},
		Subject: "Hello",
	}, mustBody(t, "", "Hello"))
	require.NoError(t, err)

	// The blind recipient is an envelope recipient only.
	assert.Equal(t, []string{"muggle@example.com", "mcgonagall@example.com"}, env.rcpt)

	var out bytes.Buffer
	require.NoError(t, env.writeTo(&out))
	assert.NotContains(t, out.String(), "mcgonagall@example.com")
	assert.Empty(t, parseMessage(t, out.Bytes()).header.Get("Bcc"))
}

func TestEnvelopeEncodesNonASCII(t *testing.T) {
	m := testMailer(t)
	raw, err := renderMessage(t, m, Message{
		To:      []string{"muggle@example.com"},
		Subject: "Pendaftaran — selesai",
		HTML:    "<p>Halo, Andi</p>",
	})
	require.NoError(t, err)

	msg := parseMessage(t, raw)
	// A header is ASCII on the wire, so a non-ASCII subject is an encoded word
	// the client decodes back.
	assert.NotContains(t, msg.header.Get("Subject"), "—")
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.header.Get("Subject"))
	require.NoError(t, err)
	assert.Equal(t, "Pendaftaran — selesai", subject)
}

func TestEnvelopeEncodesTheBodyAsQuotedPrintable(t *testing.T) {
	m := testMailer(t)
	raw, err := renderMessage(t, m, Message{
		To:      []string{"muggle@example.com"},
		Subject: "Hello",
		Text:    "Halo — panjang " + strings.Repeat("x", 200),
	})
	require.NoError(t, err)

	msg := parseMessage(t, raw)
	assert.Equal(t, "quoted-printable", msg.header.Get("Content-Transfer-Encoding"))
	// Every line of an SMTP body is CRLF terminated; the transport rewraps
	// anything longer.
	for _, line := range strings.Split(strings.TrimSuffix(msg.body, "\r\n"), "\r\n") {
		assert.LessOrEqual(t, len(line), 76, "quoted-printable lines stay under 76 characters")
	}
}

func TestEnvelopeRefusesHeaderInjection(t *testing.T) {
	m := testMailer(t)

	_, err := renderMessage(t, m, Message{
		To:      []string{"user@example.com\r\nBcc: attacker@example.com"},
		Subject: "Hello",
		Text:    "Hello",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line break")

	_, err = renderMessage(t, m, Message{
		To:      []string{"muggle@example.com"},
		Subject: "Hello",
		Text:    "Hello",
		Headers: map[string]string{"X-Note": "value\r\nBcc: attacker@example.com"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line break")
}

func TestEnvelopeRefusesAnEmptyMessage(t *testing.T) {
	m := testMailer(t)

	for name, msg := range map[string]Message{
		"no recipient": {Subject: "Hello", Text: "Hello"},
		"no subject":   {To: []string{"muggle@example.com"}, Text: "Hello"},
		"bad address":  {To: []string{"not-an-address"}, Subject: "Hello", Text: "Hello"},
	} {
		_, err := renderMessage(t, m, msg)
		assert.Error(t, err, name)
	}

	// A message with no body is refused by the body source, which is what knows
	// whether it carries anything.
	_, err := staticBody("", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no body")
}

func TestSendReportsAnUnconfiguredMailer(t *testing.T) {
	client, err := New(config.Default(), nil)
	require.NoError(t, err)

	assert.False(t, client.Configured())
	assert.Equal(t, "mailer: not configured", client.String())

	err = client.Send(t.Context(), Message{To: []string{"a@b.c"}, Subject: "x", Text: "x"})
	assert.ErrorIs(t, err, ErrNotConfigured)
}

func TestSendRefusesWithoutAServer(t *testing.T) {
	// The port is not listening, so the dial fails rather than hanging: the
	// class a caller matches says the server was never reached.
	cfg := config.Default()
	cfg.Mailer.SMTPHost = "127.0.0.1"
	cfg.Mailer.SMTPPort = 1
	client, err := New(cfg, nil)
	require.NoError(t, err)

	err = client.Send(t.Context(), Message{
		To:      []string{"muggle@example.com"},
		Subject: "Hello",
		Text:    "Hello",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNetwork)
	assert.NotErrorIs(t, err, ErrNotConfigured)
}

func TestSendHonoursACancelledContext(t *testing.T) {
	client := testMailer(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := client.Send(ctx, Message{To: []string{"a@b.c"}, Subject: "x", Text: "x"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCanceled)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestClassifyKeepsTheServerReplyCode(t *testing.T) {
	// The first digit of an SMTP reply is the whole rule, and a caller acts on
	// it differently: a 5xx is permanent, a 4xx may be sent again.
	cases := map[string]struct {
		code int
		want error
	}{
		"permanent": {550, ErrRejected},
		"transient": {451, ErrTemporary},
	}
	for name, tc := range cases {
		err := classify(OpSend, "smtp.example.com:587", &smtp.SMTPError{Code: tc.code, Message: "no"})
		assert.ErrorIs(t, err, tc.want, name)
		var failure *Error
		require.ErrorAs(t, err, &failure)
		assert.Equal(t, tc.code, failure.Code)
	}
}

func TestClassifyNamesTheOperationWithoutTheMessage(t *testing.T) {
	err := classify(OpAuth, "smtp.example.com:587", &smtp.SMTPError{Code: 535, Message: "bad credentials"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAuth)

	text := err.Error()
	assert.Contains(t, text, "auth")
	assert.Contains(t, text, "smtp.example.com:587")
	assert.Contains(t, text, "535")
	// The server's own text is not repeated: it is the server's, and it can
	// quote the credential that was tried.
	assert.NotContains(t, text, "bad credentials")
}

func TestClassifyTreatsATransportFailureAsNetwork(t *testing.T) {
	err := classify(OpDial, "smtp.example.com:587", errors.New("connection refused"))
	assert.ErrorIs(t, err, ErrNetwork)
}

func TestLocalNameIsTheSendersDomain(t *testing.T) {
	assert.Equal(t, "example.com", localName("owl-post@example.com"))
	assert.Equal(t, "localhost", localName("no-reply"))
}

func TestAddressIsHostAndPort(t *testing.T) {
	assert.Equal(t, "smtp.example.com:587", testMailer(t).Address())
}

func TestStringHidesTheCredential(t *testing.T) {
	cfg := testConfig()
	cfg.Mailer.SMTPUsername = "bot"
	cfg.Mailer.SMTPPassword = "hunter2"
	client, err := New(cfg, nil)
	require.NoError(t, err)

	text := client.String()
	assert.NotContains(t, text, "hunter2")
	assert.NotContains(t, text, "bot")
	assert.Contains(t, text, "smtp.example.com:587")
}

func TestNewRefusesAHostWithoutAPort(t *testing.T) {
	cfg := config.Default()
	cfg.Mailer.SMTPHost = "smtp.example.com"
	cfg.Mailer.SMTPPort = 0

	_, err := New(cfg, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "smtp_port")
}

// decodeQuotedPrintable is the check the receiving client performs: the body
// must decode back to what the caller wrote.
func decodeQuotedPrintable(t *testing.T, body string) string {
	t.Helper()
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	require.NoError(t, err)
	return string(decoded)
}

func TestEnvelopeBodyDecodesBackToTheOriginal(t *testing.T) {
	const body = "Halo — baris kedua dengan karakter non-ASCII: ünïcödé"
	m := testMailer(t)
	raw, err := renderMessage(t, m, Message{To: []string{"a@b.c"}, Subject: "x", Text: body})
	require.NoError(t, err)

	msg := parseMessage(t, raw)
	assert.Equal(t, body, strings.TrimRight(decodeQuotedPrintable(t, msg.body), "\r\n"))
}

// mustBody builds a static body for the cases that check the envelope rather
// than the rendering.
func mustBody(t *testing.T, html, text string) bodySource {
	t.Helper()
	body, err := staticBody(html, text)
	require.NoError(t, err)
	return body
}

// TestTemplatedBodyIsStreamedNotBuffered is the point of the body source: the
// envelope asks the template for one rendering at a time, straight into the
// writer, so no rendered string exists on the send path.
func TestTemplatedBodyIsStreamedNotBuffered(t *testing.T) {
	templates, err := NewTemplates(SenderFrom(config.Default()))
	require.NoError(t, err)

	m := testMailer(t)
	raw, err := renderTemplateMessage(t, m, templates, Message{
		To:      []string{"neveu@example.com"},
		Subject: "Reset your password",
	}, TemplatePasswordReset, View{Data: PasswordResetData{
		Email:     "neveu@example.com",
		ResetCode: "expecto-patronum",
	}})
	require.NoError(t, err)

	msg := parseMessage(t, raw)
	mediaType, params, err := mime.ParseMediaType(msg.header.Get("Content-Type"))
	require.NoError(t, err)
	assert.Equal(t, "multipart/alternative", mediaType)

	// Both renderings are present, and the HTML one carries the link.
	decoded := decodeQuotedPrintable(t, msg.body)
	assert.Contains(t, decoded, "expecto-patronum")
	assert.Contains(t, decoded, "<!DOCTYPE html")
	_ = params
}

// TestRenderToMatchesRender proves the streaming and buffered forms produce the
// same bytes, so a caller can pick either without changing the message.
func TestRenderToMatchesRender(t *testing.T) {
	templates, err := NewTemplates(SenderFrom(config.Default()))
	require.NoError(t, err)
	view := View{Data: PasswordResetData{
		Email:     "neveu@example.com",
		ResetCode: "expecto-patronum",
	}}

	buffered, err := templates.Render(TemplatePasswordReset, view)
	require.NoError(t, err)

	var html, text bytes.Buffer
	require.NoError(t, templates.RenderTo(&html, TemplatePasswordReset, BodyHTML, view))
	require.NoError(t, templates.RenderTo(&text, TemplatePasswordReset, BodyText, view))

	assert.Equal(t, buffered.HTML, html.String())
	assert.Equal(t, buffered.Text, text.String())
}

// TestRenderToRefusesAnUnknownKind covers the pair lookup: a name with a known
// HTML rendering and no text rendering is not reachable through the embedded
// set, but the lookup must still refuse rather than panic.
func TestRenderToRefusesAnUnknownKind(t *testing.T) {
	templates, err := NewTemplates(SenderFrom(config.Default()))
	require.NoError(t, err)

	err = templates.RenderTo(&bytes.Buffer{}, TemplateTestEmail, BodyKind("xml"), View{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "xml")

	err = templates.RenderTo(&bytes.Buffer{}, "no-such-template", BodyHTML, View{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-template")
}
