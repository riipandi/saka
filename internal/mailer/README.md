# Mailer

Mailer is saka's outbound email path. One `*Service` pairs an SMTP client with the React Email
templates compiled into the binary; the composition root builds it from `config.Mailer` and the
process logger.

> **Relation to the registry:** `internal/registry` registers the service, `serve` resolves it
> before the listener opens, and a feature receives it. It does not construct one. The mailer is
> optional — an empty `mailer.smtp_host` builds a mailer that refuses to send, and that is not a
> deployment failure.

## Features

- **One call to send** — `Send` renders the named template and submits it; a caller supplies the
  recipients, the subject, and the template's fields
- **Two bodies, always** — every message carries the HTML and the plain-text rendering, so a client
  that refuses HTML still shows the message
- **Streamed, not buffered** — the body is rendered straight into the SMTP session's `DATA`
  command, so a rendered message is never held as a string on the send path
- **Blind recipients stay blind** — `Bcc` is an envelope recipient only and is never written into
  the headers
- **Encrypted where possible** — implicit TLS (`mailer.smtp_secure`) or STARTTLS when the server
  advertises it; a server offering neither is sent to in the clear and reported as a warning
- **A credential never travels in the clear** — authentication over an unencrypted connection is
  refused with `ErrInsecureAuth` unless the server is on this machine or
  `mailer.smtp_allow_plaintext_auth` is set. `go-sasl` has no such guard (the stdlib's `net/smtp`
  does, inside `PlainAuth`), so it is enforced here before the password is offered
- **One attempt budget** — `mailer.timeout` bounds the dial, the handshake, every command, and the
  message body. The caller's context ends the session
- **Errors a caller can match** — `ErrNotConfigured`, `ErrNetwork`, `ErrTimeout`, `ErrCanceled`,
  `ErrAuth`, `ErrInsecureAuth`, `ErrRejected`, `ErrTemporary` (`errors.Is`)
- **No credentials in a log line** — the target is `host:port`; the username and the password are
  never rendered
- **Templates checked at construction** — the compiled files are parsed as HTML/text pairs when the
  service is built, so a broken build fails the run rather than the first send
- **Escaping where it matters** — the HTML body is rendered by `html/template` (a name carrying
  markup stays text), the plain-text body by `text/template` (it is not HTML)

## Wiring

```go
service := do.MustInvoke[*mailer.Service](injector)
err := service.Send(ctx, mailer.Request{
    To:       []string{"andi@example.com"},
    Subject:  "Reset your password",
    Template: mailer.TemplatePasswordReset,
    View: mailer.View{Data: mailer.PasswordResetData{
        Email:     "andi@example.com",
        ResetLink: "https://app.example.com/reset?token=…",
    }},
})
```

A feature that only wants the SMTP half takes `service.Mailer()`; one that only wants a rendered
body takes `service.Templates()`.

## Templates

The sources live in `email/templates/*.tsx` and are compiled by the Vite build
(`plugins/plugin-email.ts`) into `web/email/<name>_{html,text}.tmpl`, which `web/embed.go` embeds.
A template's fields are the `{{.Data.…}}` names in its `TemplateProps`, and the Go-side struct that
supplies them lives in `internal/mailer/data.go`. The field names must match exactly: a Go template
resolves a field by its own name, so a renamed field fails the render rather than printing nothing.

Adding a template means a `.tsx` file, a struct in `data.go`, and a fixture in
`internal/mailer/templates_test.go` — the test renders every template the binary carries, so a
missing fixture or a renamed field fails there.

## Rendering: `Render` or `RenderTo`

`Templates.Render(name, view)` returns both renderings as strings — the convenient form for a test,
a preview, or a body sent some other way. `Templates.RenderTo(w, name, kind, view)` writes one
rendering into a writer, which is what the send path uses: the body goes into the SMTP session's
`DATA` command and never becomes a string. The two produce identical bytes
(`TestRenderToMatchesRender`).

The parsed templates **are** the cache: parsing happens once per process, in the registry, and every
send reuses it. Measured on an M2 Pro (`internal/mailer/bench_test.go`):

| Operation | Time | Allocations |
| --- | --- | --- |
| `RenderTo` one rendering (send path) | ~4.5 µs | 58, 1.5 KB |
| `Render` both renderings | ~10.4 µs | 73, 20 KB |
| Parse every template (once per process) | ~177 µs | 2318, 276 KB |

A submission costs 60–100 ms against a real server, so rendering is ~0.005% of it. There is no
second cache layer, and one would not pay: memoizing by template data would retain reset tokens in
memory past their use, and caching templates in `internal/cache` would add a network round trip for
content that is already in the binary.

## Defaults

| Key | Default |
| --- | --- |
| `mailer.from_email` | `mailer@example.com` |
| `mailer.from_name` | `Saka Mailer` |
| `mailer.smtp_host` | empty — the mailer is off |
| `mailer.smtp_port` | `587` |
| `mailer.smtp_secure` | `false` (STARTTLS when offered) |
| `mailer.smtp_allow_plaintext_auth` | `false` |
| `mailer.timeout` | 15s |

Durations in the config file are seconds.

## Authentication safety

`go-sasl` sends the password as soon as the server asks for it, with no opinion about the transport.
The stdlib's `net/smtp` refuses that case inside `PlainAuth`; `internal/mailer` refuses it in
`authenticate`, before any mechanism is chosen:

- a session over TLS (implicit or STARTTLS) is always allowed;
- a session to a loopback host (`localhost`, `127.0.0.0/8`, `::1`) is allowed, so a local Mailpit
  works without a certificate;
- anything else needs `mailer.smtp_allow_plaintext_auth` (or `MAILER_SMTP_ALLOW_PLAINTEXT_AUTH`).

The refusal is `ErrInsecureAuth`, which also matches `ErrAuth`. It is deliberately **not** a
`Validate` rule: whether a server offers STARTTLS is only known once it has been asked, and a
configuration check would reject the ordinary submission server.

## Proving it works

```sh
docker compose -f container/compose.yaml up -d mailpit
task mailer:smoke -- --to=you@example.com
```

Open <http://localhost:8025> to read the message back. `internal/mailer/integration_test.go` runs
the same path against `testutils.StartMailpit`, so it needs a Docker daemon and skips without one.
