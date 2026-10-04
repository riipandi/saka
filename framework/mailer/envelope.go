package mailer

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"slices"
	"strings"
	"time"
)

// BodyKind names one of the two renderings of a message.
type BodyKind string

const (
	// BodyHTML is the rendering a client that understands HTML shows.
	BodyHTML BodyKind = "html"
	// BodyText is the rendering a text-only client shows.
	BodyText BodyKind = "text"
)

// contentTypes maps a kind to the media type its part carries.
var contentTypes = map[BodyKind]string{
	BodyHTML: "text/html",
	BodyText: "text/plain",
}

// bodySource produces a message body. The envelope writes the header first and
// then asks the source for each rendering the message carries, so a body is
// never held in memory unless the caller built it from a string.
type bodySource struct {
	// kinds are the renderings this body carries, in the order they are written.
	kinds []BodyKind
	// write renders one kind into w. It is called once per entry in kinds.
	write func(w io.Writer, kind BodyKind) error
}

// staticBody builds a body from strings the caller already has. The text part
// comes first when both exist: a client that does not understand the HTML shows
// the last part it can, and every client understands text/plain.
func staticBody(html, text string) (bodySource, error) {
	switch {
	case html == "" && text == "":
		return bodySource{}, errors.New("mailer: message has no body")
	case html == "":
		return bodySource{
			kinds: []BodyKind{BodyText},
			write: func(w io.Writer, _ BodyKind) error { return writeString(w, text) },
		}, nil
	case text == "":
		return bodySource{
			kinds: []BodyKind{BodyHTML},
			write: func(w io.Writer, _ BodyKind) error { return writeString(w, html) },
		}, nil
	default:
		return bodySource{
			kinds: []BodyKind{BodyText, BodyHTML},
			write: func(w io.Writer, kind BodyKind) error {
				if kind == BodyHTML {
					return writeString(w, html)
				}
				return writeString(w, text)
			},
		}, nil
	}
}

// renderBody builds a body that renders a template on demand, so the message
// never holds a rendered string.
func renderBody(templates *Templates, name string, view View) bodySource {
	return bodySource{
		// The order matches staticBody: the plain rendering first.
		kinds: []BodyKind{BodyText, BodyHTML},
		write: func(w io.Writer, kind BodyKind) error {
			return templates.RenderTo(w, name, kind, view)
		},
	}
}

// envelope is a message resolved into what the SMTP session needs: the sender,
// every recipient including the blind ones, the header block, and a body that is
// written on demand.
type envelope struct {
	from     string
	rcpt     []string
	header   textproto.MIMEHeader
	boundary string
	body     bodySource
}

// errNoRecipient is returned for a message addressed to nobody. A submission
// with no recipient is refused by the server anyway; catching it here names the
// mistake at the caller.
var errNoRecipient = errors.New("mailer: message has no recipient")

// prepare validates a message and resolves everything that does not depend on
// the body's bytes, so the body can be written straight into the session.
func (m *Mailer) prepare(msg Message, body bodySource) (*envelope, error) {
	if err := msg.validate(); err != nil {
		return nil, err
	}
	if len(body.kinds) == 0 {
		return nil, errors.New("mailer: message has no body")
	}

	rcpt := make([]string, 0, len(msg.To)+len(msg.Cc)+len(msg.Bcc))
	rcpt = append(rcpt, msg.To...)
	rcpt = append(rcpt, msg.Cc...)
	rcpt = append(rcpt, msg.Bcc...)
	if len(rcpt) == 0 {
		return nil, errNoRecipient
	}

	header := textproto.MIMEHeader{}
	header.Set("From", m.fromHeader())
	// A message may carry only a Cc: a To header with no address in it is worse
	// than no To header.
	if len(msg.To) > 0 {
		header.Set("To", strings.Join(msg.To, ", "))
	}
	if len(msg.Cc) > 0 {
		header.Set("Cc", strings.Join(msg.Cc, ", "))
	}
	// Bcc is deliberately absent: it is an envelope recipient only, which is
	// what keeps it blind.
	header.Set("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header.Set("Date", time.Now().Format(time.RFC1123Z))
	header.Set("Message-ID", messageID(m.settings.FromEmail))
	header.Set("MIME-Version", "1.0")
	for name, value := range msg.Headers {
		header.Set(name, value)
	}

	env := &envelope{from: m.settings.FromEmail, rcpt: rcpt, header: header, body: body}
	if len(body.kinds) > 1 {
		boundary, err := newBoundary()
		if err != nil {
			return nil, err
		}
		env.boundary = boundary
		header.Set("Content-Type", "multipart/alternative; boundary="+boundary)
		return env, nil
	}
	// One rendering: it is the message, so it carries the content type and the
	// encoding directly. A multipart wrapper around a single part is boilerplate
	// some readers display.
	header.Set("Content-Type", contentTypes[body.kinds[0]]+"; charset=utf-8")
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	return env, nil
}

// writeTo writes the whole message — header, blank line, and body — into w.
// Nothing is buffered here: the destination is the SMTP session's DATA command
// on the send path, so a large HTML body never becomes a string.
func (e *envelope) writeTo(w io.Writer) error {
	if err := writeHeader(w, e.header); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\r\n"); err != nil {
		return err
	}
	if e.boundary == "" {
		return writeEncoded(w, e.renderer(e.body.kinds[0]))
	}
	return e.writeMultipart(w)
}

// writeMultipart writes the alternative parts, in the order the body declares.
func (e *envelope) writeMultipart(w io.Writer) error {
	mw := multipart.NewWriter(w)
	if err := mw.SetBoundary(e.boundary); err != nil {
		return err
	}
	for _, kind := range e.body.kinds {
		part, err := mw.CreatePart(partHeader(kind))
		if err != nil {
			return err
		}
		if err := writeEncoded(part, e.renderer(kind)); err != nil {
			return err
		}
	}
	return mw.Close()
}

// renderer adapts the body source to one kind.
func (e *envelope) renderer(kind BodyKind) func(io.Writer) error {
	return func(w io.Writer) error { return e.body.write(w, kind) }
}

// partHeader is the header one alternative part carries.
func partHeader(kind BodyKind) textproto.MIMEHeader {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentTypes[kind]+"; charset=utf-8")
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	return header
}

// writeEncoded runs render through the quoted-printable encoder, so a long HTML
// line survives a transport that rewraps.
func writeEncoded(w io.Writer, render func(io.Writer) error) error {
	qp := quotedprintable.NewWriter(w)
	if err := render(qp); err != nil {
		return err
	}
	return qp.Close()
}

// writeString is the static body's renderer: it writes an already-rendered body.
func writeString(w io.Writer, content string) error {
	_, err := io.WriteString(w, content)
	return err
}

// validate refuses a message the SMTP session cannot represent: no subject, an
// address with a line break, or a header that would inject one. The body is
// checked by the body source, which is what knows whether it carries anything.
func (msg Message) validate() error {
	if msg.Subject == "" {
		return errors.New("mailer: message has no subject")
	}
	for _, addr := range append(append(append([]string{}, msg.To...), msg.Cc...), msg.Bcc...) {
		if err := checkAddress(addr); err != nil {
			return err
		}
	}
	for name, value := range msg.Headers {
		if err := checkHeader(name, value); err != nil {
			return err
		}
	}
	return nil
}

// checkAddress refuses an address that would break the SMTP command or inject a
// header. The envelope is written line by line, so a line break in an address is
// the whole attack.
func checkAddress(addr string) error {
	switch {
	case strings.TrimSpace(addr) == "":
		return errors.New("mailer: recipient must not be empty")
	case strings.ContainsAny(addr, "\r\n"):
		return errors.New("mailer: recipient must not contain a line break")
	case strings.ContainsAny(addr, "<>,"):
		return fmt.Errorf("mailer: recipient %q must be a bare address", addr)
	case !strings.Contains(addr, "@"):
		return fmt.Errorf("mailer: recipient %q is not an email address", addr)
	default:
		return nil
	}
}

// checkHeader refuses a header name or value holding a line break, which is how
// a caller-supplied header would add one of its own.
func checkHeader(name, value string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("mailer: header name must not be empty")
	case strings.ContainsAny(name, "\r\n: "):
		return fmt.Errorf("mailer: header name %q is not a field name", name)
	case strings.ContainsAny(value, "\r\n"):
		return fmt.Errorf("mailer: header %s must not contain a line break", name)
	default:
		return nil
	}
}

// fromHeader renders the sender as the display name and the address.
func (m *Mailer) fromHeader() string {
	if m.settings.FromName == "" {
		return m.settings.FromEmail
	}
	return mime.QEncoding.Encode("utf-8", m.settings.FromName) + " <" + m.settings.FromEmail + ">"
}

// writeHeader renders the header block. multipart.Writer sorts and writes its
// own part headers, but the message header is written here, in the order a
// reader expects.
func writeHeader(w io.Writer, header textproto.MIMEHeader) error {
	for _, name := range headerOrder(header) {
		for _, value := range header[name] {
			if _, err := fmt.Fprintf(w, "%s: %s\r\n", name, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// headerOrder lists the header names in the order they are written: the fields a
// reader looks at first, then whatever the caller added, sorted so two runs
// produce the same message.
func headerOrder(header textproto.MIMEHeader) []string {
	preferred := []string{
		"From", "To", "Cc", "Subject", "Date", "Message-ID",
		"MIME-Version", "Content-Type", "Content-Transfer-Encoding",
	}
	seen := make(map[string]bool, len(header))
	order := make([]string, 0, len(header))
	for _, name := range preferred {
		if _, ok := header[name]; ok {
			order = append(order, name)
			seen[name] = true
		}
	}
	extra := make([]string, 0, len(header))
	for name := range header {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	slices.Sort(extra)
	return append(order, extra...)
}

// newBoundary returns a multipart boundary that cannot appear in the bodies.
// A random one is what makes that true: a boundary derived from the content
// could be produced by the content.
func newBoundary() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mailer: boundary: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// messageID builds the Message-ID a receiving server records. The domain is the
// sender's, so the identifier belongs to the deployment that sent the message.
func messageID(from string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("<%d@%s>", time.Now().UnixNano(), localName(from))
	}
	return fmt.Sprintf("<%s@%s>", hex.EncodeToString(b[:]), localName(from))
}
