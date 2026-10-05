# The foundation for what's next

[![Go](https://img.shields.io/badge/Go-1.27-blue.svg?logo=Go&logoColor=white)](https://go.dev)
[![PostgreSQL](https://img.shields.io/badge/Postgres-18-blue.svg?logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![TypeScript](https://img.shields.io/badge/TypeScript-5.9-blue.svg?logo=typescript&logoColor=blue)](https://www.typescriptlang.org)
[![React](https://img.shields.io/badge/React-19-blue.svg?logo=react)](https://react.dev)
[![Release](https://img.shields.io/github/v/release/riipandi/saka?logo=docker&logoColor=white)](https://github.com/riipandi/saka/releases)
[![Contribution](https://img.shields.io/badge/Contributions-welcome-gray.svg?labelColor=green)](https://github.com/riipandi/saka/graphs/contributors)
<!-- [![CI Test](https://github.com/riipandi/saka/actions/workflows/test.yml/badge.svg)](https://github.com/riipandi/saka/actions/workflows/test.yml) -->
<!-- [![CI Release](https://github.com/riipandi/saka/actions/workflows/release.yml/badge.svg)](https://github.com/riipandi/saka/actions/workflows/release.yml) -->

---

**Saka** — an enterprise-ready solid foundation for scalable applications,
unified identity, and modular architecture. A project boilerplate with
authentication built in: clone it, and the hardest part of a new product
— accounts, sign-in, sessions, permissions — already works, tried and
tested. Build your product around the auth core; or deploy it standalone
as a pure identity provider for your other applications.

The auth works in both directions out of the box:

- **Saka as the IdP** — other applications sign their users in with Saka
  accounts over OpenID Connect.
- **Saka as the SSO client** — users sign in to Saka itself with Google,
  GitHub, or any OpenID Connect provider you connect.

The architecture is a **modular monolith**: features live in isolated
modules with clear domain boundaries, while deployment stays a single
binary. It follows the [twelve-factor app](https://12factor.net/)
methodology — strict config separation, stateless processes, backing
service abstraction — so a future service extraction is a refactor, not
a rewrite.

> [!WARNING]
> This project is under active development, so you may encounter bugs.
> Please review the release notes thoroughly before updating, as breaking
> changes can occur — use at your own risk!

## Features

**The auth core** — the part most projects build last and regret:

- **Built-in authentication**: password, passkeys (WebAuthn), one-time
  email codes, device pairing login — every account may carry several
- **MFA**: TOTP with printable recovery codes, honored on *every* sign-in
  path; step-up proofs for sensitive actions
- **OAuth SSO**: sign in with Google, GitHub, or custom OpenID Connect
  connections (discovery-based), with account linking, JIT sign-up, and
  email-code verification gates
- **OpenID provider**: other apps sign in with Saka accounts — code flow
  with PKCE S256, PAR, device flow, refresh rotation, introspection,
  revocation, RP-initiated and back-channel logout; the four certification
  profiles rehearse green against the OpenID Foundation suite
- **Sessions**: multi-device with per-session revocation, admin
  impersonation (delegated, audited), anti-enumeration recovery flows
- **Authorization**: role and permission grants checked against a catalog;
  users, groups, sign-up modes (open/invite/closed), blocklist
- **API keys**: machine credentials with shown-once secrets, renewal, and
  soft revocation

**The platform around it:**

- **Single-binary deployment**: backend, frontend, email templates, and
  migrations in one Go binary
- **API**: ConnectRPC (snake_case JSON) + REST, type-safe by code
  generation; wire-level documentation in `docs/`
- **Storage**: buckets, resumable (tus) uploads, local or S3-compatible
  backends, signed links
- **Webhooks**: signed (HMAC-SHA256) event deliveries with retries and an
  SSRF guard
- **Notifications**: in-product announcements with audiences, receipts,
  and a live stream
- **SCIM**: outbound provisioning per client (hourly + change-driven)
- **Audit log**: every security-relevant act, retained by policy
- **Observability**: structured logs, OpenTelemetry traces and metrics
  (Prometheus endpoint), health/readiness
- **Database**: PostgreSQL (pgx), goose migrations with UUIDv7 keys
- **Testing**: Testcontainers integration tests, wire-level E2E ladders,
  a k6 load test, and the conformance-suite driver
- **Developer workflow**: `task` runner, Docker Compose dev stack
  (Postgres, Mailpit, optional Valkey), Vite+ toolchain, debug-build
  devtools

## Quick Start
Read the [CONTRIBUTING.md](./CONTRIBUTING.md) for detailed guidelines on contributing to this project.

### Up and Running

Requirements: Go 1.27+, Node 24.21+, pnpm, and Docker.

```sh
# Clone the repository
git clone https://github.com/riipandi/saka
cd saka

pnpm install              # frontend dependencies
task deps                 # Go toolchain binaries (golangci-lint, goose, …)
task config:generate      # write app.config.json, then fill in the secrets
cp .env.example .env.local && edit .env.local
task compose:up           # start local dev services (postgres, mailpit, …)
task db:initialize        # migrations + system seed + first administrator
task dev                  # the dev loop: Vite + HMR behind the Go proxy
```

**The application will be accessible at:**

- Application: <http://localhost:3080> (API and frontend on one origin)
- Mail inbox: <http://localhost:8025> (Mailpit)
- Health: <http://localhost:3080/healthz>

**Available task commands:**

- `task dev` - Run the dev loop (Vite + HMR behind the Go proxy)
- `task serve` - Run the server (debug build)
- `task build` - Build the application binaries
- `task lint` / `task test` / `task check` - The everyday checks
- `task --list` - The full list (it mirrors the CLI)

## Documentation

| Page | What it covers |
| --- | --- |
| [Product guide](./docs/product-guide.md) | The whole product in one walk |
| [Authentication](./docs/auth.md) | The sign-in methods, MFA, sessions — in depth |
| [OAuth SSO](./docs/oauth-sso.md) | Connecting external providers |
| [OpenID provider](./docs/oidc.md) | How other apps sign in with Saka |
| [Configuration](./docs/configuration.md) | The config file, secrets, settings |
| [Deployment](./docs/deployment.md) | Running Saka for real |

More in [`docs`](./docs): storage, webhooks, notifications, API keys,
SCIM, the conformance profiles, the debug utilities, and the API
reference.

## License

This project licensed under the [Apache License 2.0][license-apache].
See [LICENSE](./LICENSE) and [NOTICE.md](./NOTICE.md) for more information.

---

<sub>If you like my work, you can support me via [GitHub sponsors](https://github.com/sponsors/riipandi).</sub>

[![Creator Badge](https://badgen.net/badge/icon/by%20Aris%20Ripandi?label&color=black&labelColor=black)][riipandi-x]

[license-apache]: https://choosealicense.com/licenses/apache-2.0/
[riipandi-x]: https://twitter.com/intent/follow?screen_name=riipandi
