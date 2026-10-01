[![Go](https://img.shields.io/badge/Go-1.27-blue.svg?logo=Go&logoColor=white)](https://go.dev)
[![TypeScript](https://img.shields.io/badge/TypeScript-7.0-blue.svg?logo=typescript&logoColor=blue)](https://www.typescriptlang.org)
[![React](https://img.shields.io/badge/React-19-blue.svg?logo=react)](https://react.dev)
[![Postgres](https://img.shields.io/badge/Postgres-18-blue.svg?logo=postgresql)](https://www.postgresql.org)

Starter project template built with [Go][golang], [chi][go-chi] (HTTP router), [ConnectRPC][connectrpc] (RPC),
[Postgres][postgres] (database), [React][react], and [TanStack][tanstack] (Router, Query, Store). It exists so you
can start building without repeating the initial setup.

---

> [!NOTE]
> This project is a template I use for my personal use, so you may encounter bugs.
> Please review the release notes thoroughly before updating, as breaking changes can occur!

## Quick Start

You will need [`Go >= 1.27`][golang], [`Node.js >= 24.21`][nodejs], [`PNPM >= 12.6`][pnpm], and
[`Docker >= 20.10`][docker] installed on your machine.

```bash
pnpm dlx tiged riipandi/tango myapp-name
```

1. Install the Go toolchain binaries: `task deps`
2. Find and replace `tango`, `Tango`, and `MyApplication` across the source files.
3. Install the frontend dependencies: `pnpm install`
4. Write a starter config file: `task config:generate`
5. Generate the secret keys into your env file: `task key:generate`
6. Start the local Postgres: `docker compose -f docker/compose.yaml up -d pgsql`
7. Set `DATABASE_URL` in `.env.local` (see `.env.example`)
8. Run the database migrations: `task db:migrate`
9. Bootstrap the system seed, the signing key pair, and the first administrator: `task db:initialize` (see `scripts/task-database.yml`; wraps `tango initialize`)
10. Start the development servers: `task dev`

Vite serves the frontend on `:3000` and proxies `/api`, `/rpc`, `/.well-known`, `/metrics`, and `/static` to Go on `:3080`.
Go files are watched and rebuilt automatically.

## Available Tasks

Run `task` to list every target. Most targets live in `scripts/task-*`, one file per group; the root
`Taskfile.yml` declares the shared variables, includes them (flattened — `task db:migrate`, never
`task database:db:migrate`), and keeps the `compose:*` targets itself.

| Command             | Description                                            |
| ------------------- | ------------------------------------------------------ |
| `task dev`          | One origin (:3080) — Go serves the document; Vite compiles + HMR behind it; the binary rebuilt from `build/debug` on every Go change |
| `task run`          | Run the CLI directly (debug build)                     |
| `task build`        | Build the frontend and both Go binaries                |
| `task start`        | Run the production binary                              |
| `task test`         | Run the frontend and backend tests                     |
| `task typecheck`    | Run TypeScript type checking                           |
| `task lint`         | Run all linters (Go and JS)                            |
| `task check`        | Run `go vet` and the formatting check                  |
| `task format`       | Format all files (Go and JS)                           |
| `task key:generate` | Generate the application secret keys                   |
| `task cert:generate`| Generate local HTTPS certificates into `storage/config` |
| `task cert:trust`   | Trust the local CA in the system trust store           |
| `task rpc:generate` | Generate Go and TypeScript from the proto contracts    |
| `task rpc:stale`    | Fail when generated code is out of date                |
| `task metrics:up`   | Start the observability stack (OpenTelemetry)          |
| `task compose:up`   | Start the docker compose services                      |
| `task compose:down` | Stop the docker compose services                       |

## The Short Version

- **Configuration** — `app.config.json` is the single source of truth. Precedence: built-in
  defaults → config file → CLI flags. The environment is not a layer: a variable reaches a key
  only where the file references it (`env:NAME` or `${NAME}`), and secrets are never written
  literally. Details: [`internal/config/README.md`](./internal/config/README.md).
- **Database** — goose migrations embedded in the binary (`database/migrations/`), tracked in
  `app_migration`, applied over a single locked connection. Migrate, seed, and inspect with the
  `db:*` / `migrate:*` commands. Details: [`database/README.md`](./database/README.md).
- **Dump and restore** — `task db:export` writes a readable SQL dump (`COPY` blocks, PK-ordered,
  optional `--compression`); `task db:import` loads it back in one transaction, reordered by
  foreign keys. See `database/README.md` for the flags.
- **Logging** — `log/slog` is the only frontend; `log.transport` names the sinks (console,
  rotating file, OTLP collector). Details: [`internal/logger/README.md`](./internal/logger/README.md).
- **Tracing and metrics** — OpenTelemetry, opt-in per signal, one collector address
  (`otel.endpoint`), plus a Prometheus exposition. Details:
  [`internal/observer/README.md`](./internal/observer/README.md).
- **Health** — `task health` and `GET /api/healthz` report the same aggregated status
  (postgres, storage; optional checks are reported but never fail the aggregate). Details:
  [`internal/health/README.md`](./internal/health/README.md).
- **Cache & key-value store** — the cache is off by default; the optional Valkey backend
  (`kvstore.enable`) is never required. Details: [`internal/cache/README.md`](./internal/cache/README.md).
- **Outbound HTTP** — one client for external services, with timeouts, retries, and a
  circuit breaker. Details: [`internal/fetcher/README.md`](./internal/fetcher/README.md).
- **Email** — templated messages sent over SMTP, with the React Email templates compiled into the
  binary. Optional, so a local checkout needs no mail server. Details:
  [`internal/mailer/README.md`](./internal/mailer/README.md).
- **File storage** — chunked, content-addressed uploads over the local data directory or S3,
  manifest in Postgres, uploads on the durable queue. Details:
  [`internal/storage/README.md`](./internal/storage/README.md).
- **Queue & scheduler** — a durable Postgres task queue with retries and a dead-letter archive
  ([`internal/queue/README.md`](./internal/queue/README.md)), driven by a cron scheduler whose
  claimed tick and enqueued task share one transaction
  ([`internal/scheduler/README.md`](./internal/scheduler/README.md)).

## Local HTTPS

The nginx service in `docker/compose-dev.yaml` reads certificates from `storage/config`. Generate them with
[`mkcert`](https://github.com/FiloSottile/mkcert), falling back to a self-signed `openssl` certificate when
`mkcert` is unavailable:

```sh
# Writes storage/config/localhost_key.pem and storage/config/localhost_crt.pem.
task cert:generate

# Install the mkcert local CA in the system trust store.
task cert:trust
```

`task cert:generate` covers `localhost`, `127.0.0.1`, `::1`, `host.docker.internal`, and
`*.localhost.test`. `storage/` is gitignored, so the certificates stay local.

## Deployment

Build the image with `task docker:build`. Read the [Deployment Guidelines](./docs/deployment.md) for the full procedure.

## License

Licensed under either of [Apache License 2.0][license-apache] or [MIT license][license-mit] at your option.

> Unless you explicitly state otherwise, any contribution intentionally submitted for inclusion in this project by you,
> as defined in the Apache-2.0 license, shall be dual licensed as above, without any additional terms or conditions.

Copyrights in this project are retained by their contributors. See the [LICENSE-APACHE](./LICENSE-APACHE) and
[LICENSE-MIT](./LICENSE-MIT) files for more information.

---

<sub>If you like my work, you can support me via [GitHub sponsors](https://github.com/sponsors/riipandi).</sub>

[![Creator Badge](https://badgen.net/badge/icon/by%20Aris%20Ripandi?label&color=black&labelColor=black)][riipandi-x]

[connectrpc]: https://connectrpc.com/
[docker]: https://docs.docker.com/engine/install/
[go-chi]: https://github.com/go-chi/chi
[golang]: https://go.dev/doc/install
[license-apache]: https://choosealicense.com/licenses/apache-2.0/
[license-mit]: https://choosealicense.com/licenses/mit/
[nodejs]: https://nodejs.org/en/download
[pnpm]: https://pnpm.io/installation
[postgres]: https://www.postgresql.org/
[react]: https://react.dev/
[riipandi-x]: https://twitter.com/intent/follow?screen_name=riipandi
[tanstack]: https://tanstack.com/
