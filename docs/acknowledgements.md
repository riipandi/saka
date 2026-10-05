# Acknowledgements

Saka stands on ideas others built first. This page names them.

## The projects we adapted

| Project | What Saka took from it | Where it shows |
| --- | --- | --- |
| [Pocket ID](https://github.com/pocket-id/pocket-id) | The reference implementation the whole identity surface ports: the OIDC provider's behavior, the passkey-first design, the audit vocabulary, the settings and email shapes | The OpenID provider, the account surfaces; every deliberate departure is recorded in the development notes |
| [Clerk](https://clerk.com) | The sign-in flow semantics: one flow that signs in, links, or creates — with the pauses (verified email, missing names) and the second-factor bridge | [OAuth SSO](oauth-sso.md) and the account lifecycle flows |
| [Backlite](https://github.com/mikestefanello/backlite) | The durable queue's core design (MIT), adapted for PostgreSQL and this architecture | The queue engine (`framework/queue`) |
| [tus](https://tus.io/protocols/resumable-upload) / [tusd](https://github.com/tus/tusd) | The resumable-upload protocol (tus 1.0.0 — core, creation-with-upload, termination, expiration), with the handler adapted from tusd's approach for this engine | The resumable-upload surface (`framework/storage/tus.go`, `tus_handler.go`, served at `/api/uploads`) |
| [River](https://github.com/riverqueue/river) | The periodic-job shape the scheduler follows — minus the leader election and the Pro-only durable state | The scheduler (`framework/scheduler`) |
| [robfig/cron](https://github.com/robfig/cron) | The cron spec parser and schedule arithmetic | The scheduler's timing |

## The building blocks

- [Go](https://go.dev) · [chi](https://github.com/go-chi/chi) ·
  [pgx](https://github.com/jackc/pgx) ·
  [goose](https://github.com/pressly/goose) ·
  [samber/do](https://github.com/samber/do) ·
  [koanf](https://github.com/knadh/koanf) ·
  [LogLayer](https://loglayer.dev) behind `log/slog` ·
  [OpenTelemetry](https://opentelemetry.io)
- [ConnectRPC](https://connectrpc.com) ·
  [protovalidate](https://protovalidate.com) ·
  [golang-jwt](https://github.com/golang-jwt/jwt) ·
  [jwx](https://github.com/lestrrat-go/jwx) ·
  [go-oidc](https://github.com/luikyv/go-oidc) (the OpenID provider
  engine, OpenID-certified) ·
  [x/oauth2](https://pkg.go.dev/golang.org/x/oauth2) +
  [go-oidc/v3](https://github.com/coreos/go-oidc) (the SSO client side) ·
  [go-webauthn](https://github.com/go-webauthn/webauthn)
- [Vite+](https://viteplus.dev) (Vite, Vitest, Oxlint, Oxfmt — the
  monorepo toolchain) · [React 19](https://react.dev) ·
  [TanStack](https://tanstack.com) ·
  [React Email](https://react.email)
- [Testcontainers](https://testcontainers.com) ·
  [Playwright](https://playwright.dev) ·
  [k6](https://k6.io) ·
  [Task](https://taskfile.dev) ·
  [Docker Compose](https://docs.docker.com/compose/) ·
  [Mailpit](https://github.com/axllent/mailpit)

## The people

Saka is maintained by [Aris Ripandi](https://github.com/riipandi) — with
thanks to every contributor of the projects above, whose public work
made a boilerplate like this possible.

---

Back to [Documentation Index](./index.md)
