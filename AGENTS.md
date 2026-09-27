# AGENTS.md

Modular-monolith Go boilerplate (`tango`): one binary serving HTTP/ConnectRPC API, embedded SPA, and CLI.

Rules live here; per-package reasoning, invariants, and traps live in **`.llms/architecture.md`** — read the section for the package you're changing before assuming behavior.

## Overview

- Built from `cmd/` using `urfave/cli/v3`. Implemented commands: `serve` (HTTP surface, SPA mount, metrics, middleware), `key:generate`, `health`, all `migrate:*` + `migrate:seed` (debug build), `db:export`/`db:import`, `mailer:smoke`, `auth:promote`. Everything else prints `not yet implemented` — never document, test, or rely on a stub as working.
- SPA (React 19 + TanStack + Vite) builds into `web/output/` and embeds into the binary. Vite dev server proxies `/api`, `/rpc`, `/.well-known`, `/static` to Go on `:3080`.
- Implemented packages: `database`, `internal/{audit,authz,config,datastore,guard,health,logger,cache,fetcher,mailer,queue,jobs,scheduler,storage,transport}`, `pkg/{crypto,envfile,jwtutils,responder,validate,testutils,printext}`, `api/connect`, `email/templates`, `web`, all of `modules/identity` (signin, session, signup, user, usergroup, authorization, verification, onetimeaccess, multifactor, password, jwks), `modules/auditlog`, `modules/apikey`, `modules/notification`. Scaffolds only: rest of `internal/**` and `modules/**`.
- Pocket ID is passkey-only — no passwords, no TOTP. Surfaces it never had are tango-only (`[Tango]` in Yaak); design reference for auth flows beyond ported endpoints is Better Auth.
- Never add, remove, or rename top-level packages/directories without explicit request; extend existing packages.
- Porting a plan/doc into code means implementing it, not copying it. Docs may describe larger surface than the code has.

## Stack

- Go 1.27.1, Node >= 24.21, pnpm 12.5.1, Docker (testcontainers), `task`.
- chi, Postgres via pgx, optional valkey, `samber/do` DI, goose as library, koanf JSON config, LogLayer with `log/slog` adapter, OpenTelemetry, S3 + local storage.
- Optional backends are opt-in: cache defaults to in-memory; session store and rate limit default to Postgres (`public.rate_limits` + `fn_check_rate_limit` under advisory locks).
- Logging: application code logs through `log/slog`; LogLayer is wired as the slog handler (`integrations/sloghandler`), never called directly in feature code.
- DI is explicit, composed in `internal/registry` as `do.Package` values — no global singletons, no package-level `init`. Use skill `golang-samber-do`. Layout: `infrastructure.go` names no module; `modules.go` holds only the `Area` seam (`{Name, Package, Mount}`); each area owns its wiring in `modules/<area>/module.go`. `registry.New(..., extra ...Area)` lets a consumer serve its own area without editing the registry.
- Service visibility: a service another area or the transport resolves registers in `infrastructure.go` (root); an area's `Package` holds only what its own features consume. Two areas sharing a service is the signal to promote it to infrastructure. `do.Scope` stays unused until an area must hide a service.
- A provider only constructs: no database I/O, no seeding, no network dial beyond the dependency it resolves. A step that touches state is its own service or runner, so the prewarm walk orders it and its failure fails the run.

## Build / Test / Lint

- `task dev` — Vite (:3000) + Go API (:3080). `task run` — Go CLI, debug build, `--env-file=.env.local`. `task build` — one Vite pass, emits both Go binaries (email templates embed into both). `task start` — release binary. E2E probes run against `build/release/tango serve --env-file=.env.local` — a green unit suite does not exercise the composition root.
- `task test` — frontend + Go. `task test:go -- ./modules/...` for one package. `task lint` — golangci-lint + oxlint. `task check` — `go vet` + `migrate:validate` + format. `task format` — gofmt + oxfmt. `task typecheck` — `tsc -b --noEmit`.
- Dev database reset: `task db:reset -- --force --up` then `task db:seed -- --force`. Never hand-drop tables; never reset a database holding state you did not create.
- `task rpc:generate` regenerates Go + TypeScript from `api/connect/*.proto`; `task rpc:stale` fails when contracts changed without regenerating.
- Task targets wrap CLI 1:1 (`db:migrate` → `migrate:up`), so a renamed command silently breaks the task. Extra flags after `--`.
- `task cert:generate`/`cert:trust` — `storage/certs/localhost_{key,crt}.pem` via mkcert (self-signed `openssl` fallback); read by nginx in `docker/compose-dev.yaml`.
- Integration tests use `pkg/testutils.StartPostgres/StartMailpit/StartMinIO/StartValkey/StartVictoriaLogs`. Call `testutils.SkipWithoutDocker(t)` first so no daemon skips instead of fails. Never widen timeout to work around a hung container.

## Architecture

Implemented today — treat as the contract.

- `database/` — goose SQL migrations are the single source of schema truth; never embed DDL or create tables at runtime. `migrator.go` (goose v3 under plain structs, one pinned connection), `validate.go` (no-DSN check), `create.go` (skeleton writer), `dump.go`/`backup.go`/`schema_dump.go`/`restore.go` (COPY text format, PK-ordered, idempotent replay), `compress.go` (`none|gzip|zlib|zip` + byte-level detection), `seeders/` (idempotent, caller-managed transaction).
- `modules/<area>/module.go` — the area module: feature list, one router per feature, `Deps` struct of resolved services; the area owns `Package` + `Mount` so the registry never learns its internals.
- `modules/<area>/<feature>/schema.go` — table constants + row structs; columns are named where their query is (use `sqlbuilder.NewStruct` for full CRUD), never one constant each.
- `modules/identity/multifactor` — the TOTP second factor (`tango.authn.v1.MultifactorService`): multi-device enrollment (max 10, `totp_` TypeID, secret sealed with the shared `enc:` cipher), a two-phase sign-in (password success mints a 5-minute pending bridge — hashed row, several live per account — that `CompleteSignIn` spends with a TOTP or recovery code on the full pair), one recovery set per account (10 hashed single-use codes, shown exactly once), replay guard via `last_used_step` compare-and-set, a 3-wrong-codes budget per bridge, and a second-factor proof required for disable/regenerate/delete-last. The sign-in fork runs through the `mfaGate` interface wired post-construction. Full reasoning in `.llms/architecture.md`.
- `modules/identity/jwks` — key set at `GET /.well-known/jwks.json`: configured key pair (`crypto.DecodeJWK`) + active `public.jwks` signing rows, behind `*jwks.Service` satisfying `jwtutils.KeyProvider`. Dual-stack signing (key pair + HMAC `auth.secret_key`; optional `auth.jwt_algorithm` decides only when both configured). Bare RFC 7517 JWK Set, not envelope; symmetric key never published; unreadable/mismatched key fails the run; module without provider fails closed.
- `modules/notification` — the notification area (`tango.notification.v1.NotificationService`): admin-authored notifications over three audience kinds (`global`, `users`, `user_groups` junctions — resolved by query, never fanned out at create), per-account read receipts as the whole read state, one shared visibility predicate behind the inbox, the unread count, the mark-read gate, and the email pass. `system` notices target accounts and never carry a topic or a global audience; `announcements` default to global with an optional topic. Live tail is `WatchNotifications` (server-streaming over an in-process broker, ping keepalive, no history — catch-up via List) with its path exempted from the request deadlines via `middleware.UnboundedFor`. The email pass is an `EmailDispatcher` seam over the `notification_email` queue, gated at enqueue by `mailer.notifications.announcement_email_enabled`; the processor in `internal/jobs` reads the audience through this package's repository. Reasoning in `.llms/architecture.md`.
- `pkg/crypto` — AES-256-GCM `Cipher` (`enc:` prefix), PHC `PasswordHasher`, `KeyGenerator` (all four key variables), `DecodeJWK`, `ParseHMACKeyHex` (distinct from `ParseKeyHex`). Do not add another format.
- `pkg/printext` — colour/formatting for human output; `Style` is meaning, `Palette` is colour. `pkg/envfile` — dotenv reader/writer, preserves comments and key order.
- `pkg/responder` / `pkg/validate` — API envelope + request validation; no hand-built envelopes. `pkg/jwtutils` — typed JWT claims. `pkg/testutils` — shared testcontainers.
- `internal/datastore` — single `pgxpool`, `Querier`/`WithTx`, `OpenMigrationDB`, optional Valkey client. Startup probe retries on `database.connect_attempts` × `database.connect_retry_interval` (a permanent SQLSTATE verdict ends the wait at once); the retry is the caller's to ask for — `PostgresOptions.ConnectAttempts` defaults to 1.
- Authorization is declarative: a handler never checks a role. `jwtutils.Caller` (subject + claims + `ActorID`/`ActorUsername` for delegation) is what `jwtutils.CallerFrom(ctx)` answers; `IsImpersonating()` refuses a delegated caller on `Self` and admits it on `Admin`/`Authenticated`.
- `internal/cache` — one `Cache` contract; Noop when caching does not run; in-memory driver and Valkey driver (`tango:cache:` prefix).
- `internal/fetcher` — outbound Resty client; timeout, jittered backoff, circuit breaker; typed errors (network/timeout/cancel/retries/circuit/status); bodies and credentials never logged.
- `internal/health` — check names generic (`database`, `kvstore`, `storage`), never the product; no DSN/URL/path in published form.
- `internal/logger` — LogLayer behind `*slog.Logger`; sinks `console|file|otlp`; `Slog()` is the only frontend.
- `internal/mailer` — SMTP submission + compiled React Email templates behind `*mailer.Service`; optional (empty host = refusing service); body streamed into `DATA`, never held as a string; plaintext auth refused off-loopback (`mailer.smtp_allow_plaintext_auth` opts in).
- `internal/observer` — traces + metrics; explicit exporter options (no env fallback); Prometheus bridge at `otel.metrics.prometheus_path`.
- `internal/queue` — durable Postgres task queue (SKIP LOCKED, priority, dead letters, optional payload encryption). `internal/jobs` — concrete jobs; recurring ones re-enqueue. `internal/scheduler` — durable cron; row lock claims a tick, the queue executes it.
- `internal/storage` — chunked file engine over local FS or S3; content-addressed chunks, manifest in Postgres, staging watcher, upload on durable queue; `storage.Key` keys, JSONB per-file metadata, resumable uploads, progress counters (endpoint stubbed — see TODO(notification)); `BeforeSyncHook`/`AfterSyncHook` extension points.
- `internal/transport/static` — serves `storage.local_path/uploads` at `/static`, outside the throttled group, before the SPA (missing upload = 404, never `index.html`); driver behind `Upload` (`Local` today); never lists a directory; deliberately not the file engine's tree — `files/` is never served, a feature wanting a URL writes a whole copy under `uploads/`.
- `internal/guard` — the authorization policy: `ProcedureRules` (procedure path → rule) and `RestRules` (method + pattern → rule), read by the RPC guard interceptor and by the REST bearer middleware. A request the tables do not name is **administrative** (the safe default); `Public`, `Authenticated`, `Self(field)`, `Session`, `StopImpersonating`, `Admin`, and `Permission(slug)` are the rules; a refusal is `unauthenticated` (401) or `not_found` (404), never `permission_denied`.
- RBAC is Unkey-style and flat: a permission is the code-declared slug `resource:id:action` (`*` in the instance position only, `internal/authz` — the catalog is seeded and served read-only, never admin-managed), a role is a named set, and an account's grants are its roles' plus its direct ones (`public.roles/permissions/role_permissions/user_roles/user_permissions`, migration `00011`). `user.LoadGrants` is the one door every token mint uses; the access token carries `roles` + `permissions` claims, and a grant change lands at the next mint or refresh. `users.is_admin` is gone — `guard.Admin` reads the `administrator` system role claim, the seeder grants it to the default account, and `auth:promote` bootstraps it. Full reasoning in `.llms/architecture.md` → `### authorization`.
- `internal/audit` — the audit trail's writer and vocabulary: event names, the `Entry` a caller fills, the `ClientInfo` the transport captures, and the nil-safe `Recorder`. A record is written **in the transaction that caused it** (`Record(ctx, db, entry)` takes the query surface), and the client facts plus the impersonation actor come from the request context, so a feature names neither. `modules/auditlog` is the read-only area over it — the split is forced, because the reader imports `modules/identity/user` and the transport imports no module. Retention: `audit.retention_days` + the `audit_cleanup` job.
- `internal/kernel`, `internal/transport`, `internal/registry` — full contracts in `.llms/architecture.md`.
- `api/connect/*.proto` — ConnectRPC contracts. `email/templates` — React Email sources. `web` — SPA embed and static serving.

## Library Documentation

- Look up the ACTUAL library docs first (MCP Context7/DeepWiki or official pages). Pinned source of truth is the module cache (`go env GOMODCACHE`); when docs and code disagree, code wins — say so.
- Settled library comparisons live in `.llms/architecture.md` → "Library decision records"; record new outcomes there.
- OTel `otel/log` pin (v0.19.0, three modules held for `otellog`) is fragile: after any `go get`, re-check and `go mod edit -require` them back.

## Conventions

- Config precedence (lowest→highest): defaults → JSON config file → CLI args, implemented only in `internal/config` — commands read resolved values from context (`configFrom`/`fullConfigFrom`), never `os.Getenv` or flags directly. Environment is a **value table the file references**, not a layer. Flags only via `flagBindings` (only `serve` has them); a flag that changes what a command does (`--dry-run`, `--force`) is never listed. Interpolation: `${NAME}` inline, `env:NAME` as whole value — only the file is interpolated.
- Every query against application tables is built with `go-sqlbuilder` (PostgreSQL flavor; `internal/queue/store.go` idiom). Raw SQL only in tests and `database/` maintenance tooling. A `SELECT` of a database function counts: `sb.Var(...)`, never hand-written `$1, $2, $3`.
- Every config key needs a default in `config.Default`, a rule in `config.Validate`, an entry in `public/config.schema.json` (the JSON Schema editors validate `app.config.json` against — update it in the same change as the key), and — when secret — a line in `secretKeys`. `.env.example` carries secrets + deployment variables only.
- Config files: one per stage of Config's life (`types.go`, `values.go`, `defaults.go`, `keys.go`, `file.go`, `env.go`, `flags.go`, `validate.go`, `predicates.go`, `redact.go`, `config.go`); never a file for a single function.
- Go 1.27 idioms: `encoding/json/v2` (with `omitzero`) in production code — `encoding/json` is test-only; plus `for range n`, `t.Context()` in tests, `errors.Join`, `slices`/`maps`, `min`/`max`, stdlib `uuid`. Check jsonv2 behavior deltas (<https://go.dev/doc/jsonv2-migration>) and test any a call site can carry. `DefaultOptionsV1` needs a justifying comment.
- Flattened struct literals (proposal #9859): promoted fields from anonymous embedded structs are valid literal keys — never the nesting-doll form; explicit nested form only when promoted names are ambiguous or the embedded name is the domain label; never mix promoted with enclosing keys. Migrate with `go fix -embedlit ./...`.
- Shutdown through context cancellation; long-running components implement stop and drain in-flight work.
- No compatibility shims, fallback readers, or dual-write paths — delete the obsolete path. Fix lint at the source; `//nolint` is not accepted.
- Command output: `printext.Plural`/`printext.Duration`; per-item lines indented, outcome lines at column zero under `status:` (`grep '^status:'` works); `database:` target line before touching anything; parseable commands print only their value; colour from `pkg/printext`, never for `--json`.
- Balance file size against folder depth; prioritize cohesion. Split unrelated concerns; avoid monoliths and pointless nesting.

## Comment Style

- Comments say what the code cannot: invariants, security rules, protocol requirements, side effects. Never restate code, narrate control flow, or repeat a name.
- No phase/plan markers or signatures (`phase *`, `Step N`, `TODO(plan)`); real rework gets a `TODO`/`FIXME` saying what and why. No ownership references ("owned by") — describe the invariant.
- One point per comment, as few words as the idea survives. No filler, no preamble, no narration. If halving it loses no meaning, it was too long.

## Test Naming & Copywriting

- Fixture items in tests — usernames, emails, display names, token values, placeholder text — take their names from the novels of **Dan Brown** (Robert Langdon, Sophie Neveu, Vittoria Vetra, Inferno, Digital Fortress) and **Harry Potter** (Hermione Granger, Hogwarts, Gryffindor, Horcrux, Expecto Patronum). The ordinal signup tokens follow the Harry Potter book order (`philosophers-stone`, `chamber-of-secrets`, …).
- Test function names stay behavioral (`TestSignupRejectsADuplicateAccount`) — the topic vocabulary is for the data, not the behavior they pin.

## Common Tasks

- **Add a feature module**: `modules/<area>/<feature>/` with the standard set (`schema.go`, `repository.go`, `service.go`, `handler_rpc.go`, plus `handler.go` for REST, `module.go`); implement the `internal/kernel` contract; list it in the area's `features()` — the only registration an existing area needs. Area's `Deps` carries its resolved services (never `internal/registry`). A **new** area also exports `Package` + `Mount` and adds one `registry.Areas()` entry. Business logic in `service.go`; handlers map transport only. Persistence through `internal/datastore` — never a second pool. Typed IDs from `go.jetify.com/typeid`; token/code rows store hashes plus DB `uuidv7()`.
- **Ship or change an endpoint**: update its Yaak request in the same change (see Gotchas) — strip `(unimplemented)` when it ships; keep descriptions aligned with `.llms/pocket-id-api-reference.md`. Probe against a freshly built binary before reporting it as serving — a green unit suite does not exercise the composition root.
- **Add an endpoint**: proto in `api/connect/*.proto` with request constraints as `buf.validate.field` protovalidate options (enforced once by the transport's validate interceptor); `task rpc:generate`; implement handler; mount; **declare the rule in `internal/guard`** (`ProcedureRules` for a procedure, `RestRules` for a route) — an undeclared one is administrative, so a public or self-service endpoint answers `not_found` until it is declared. A rule the contract cannot express (uniqueness, existence) stays in the feature — never duplicate a contract constraint in Go.
- **Add a migration**: `task db:create -- add_widgets`; `-- +goose Up`/`Down` mandatory; 5-digit consecutive versions above the last; goose skips `00000_*.sql` silently (`migrate:validate` and migrator tests catch it). Embedded: rebuild before it runs. Tests derive counts from `database.EmbeddedMigrations()`.
- **Add a seeder**: factory in `database/seeders/`, append to `All()` in dependency order, name `<Entity>Seeder`, guard inserts with `ON CONFLICT DO NOTHING`, report existing as skipped. `Apply` gets `datastore.Querier`; transaction is command-managed.
- **Add a dump object kind**: one writer in `dumpDDL`'s section list (`database/schema_dump.go`), restore order; prefer server deparse helpers; skip `pg_depend.deptype = 'e'`; read whole lists before per-item queries; add a round-trip case in `backup_test.go`.
- **Add a schema-touching test**: `testutils.StartPostgres(t).NewDatabase(t)` per test (shared container = one database; call `NewDatabase` again for a second). Tests querying migrated tables run the migrator first (`OpenMigrationDB` → migrator `Up` → close) then open the pool.
- **Add a config key**: default + validation in `internal/config`; `secretKeys` when secret; flag only via `flagBindings`.
- **Add a background job**: task type, `QueueConfig`, processor in `internal/jobs/<name>_job.go`, register in `internal/jobs/register.go`. Recurring jobs re-enqueue.
- **Record an audit event**: name it in `internal/audit/audit.go` (snake_case, what happened rather than which procedure answered), then `s.audit.Record(ctx, tx, audit.Entry{...})` **inside the transaction the change runs in** — pass `tx`, not `s.pool`, or the log can describe a change that rolled back. The address, the User-Agent, the fingerprint, and the impersonation actor come from the context; do not thread them through the service. The deletion case is the exception: `user_id` is `ON DELETE SET NULL`, so a record written after the row is gone must name the account in `resource_type`/`resource_id` instead.
- **Wire a cross-feature side effect or gate**: when two services need each other at build time (a gate, a notifier), define the seam as an interface in the consuming package and wire it post-construction in the area's `Package` (`users.WithBanSideEffects(...)`, `issuer.WithMFAGate(...)`) — a provider cannot take the dependency without a cycle. Never import the feature package back.
- **Post-construction of a feature needing sealed storage**: build `crypto.NewCipherFromHex(c.App.SecretKey)`; a nil cipher (test/consumer area without a key) is answered at the call site, not a failed run.
- **Add a log sink**: name in `LogTransport*`, default + `Validate` rule, build in `internal/logger`, hold resources for `Shutdown`, add a case to `TestEveryTransportShipsTheSameEntry`.
- **Prove logging end to end**: `task metrics:up`; `LOG_TRANSPORT=console,file,otlp task metrics:smoke`; `task metrics:query -- 'marker:"tango-logger-smoke"'`.
- **Prove mail end to end**: `docker compose -f docker/compose.yaml up -d mailpit`; `task mailer:smoke -- --to=you@example.com`; read back at <http://localhost:8025>. `internal/mailer/integration_test.go` covers it without compose.
- **Send an email**: render + submit in one call through `*mailer.Service` from the registry; new template needs `.tsx` in `email/templates/` + struct in `internal/mailer/data.go` matching `TemplateProps`.
- **Prove tracing/metrics**: `task metrics:up`; `OTEL_TRACING_ENABLE=true OTEL_METRICS_ENABLE=true task metrics:smoke:otel`; `task metrics:traces` and `tango_otel_smoke_total` against VictoriaMetrics.
- **Signal settings**: shared (address, identity) in `otel`; signal-specific in its own subsection; never a second collector address. `envKeys` when deployment sets it.
- **Local services**: `docker compose -f docker/compose.yaml up -d pgsql` (full stack + observability in same file); `task metrics:up` starts only the observability half.

## Gotchas

- `api/specs/` is the sync mirror of the Yaak workspace — the sync writes it, never edit by hand; change requests through the `yaak` CLI. The collection is a checklist, not the canonical reference: source of truth is the code plus `.llms/pocket-id-api-reference.md`, and a ported endpoint may change shape (some REST operations become RPC procedures). Names: `[Pocket ID]` (exact upstream summary) for ported operations, `[Tango]` for tango-only surfaces, `(unimplemented)` suffix stripped when the endpoint ships.
- `codegen/` is gitignored build output — never edit or commit; regenerate with `task rpc:generate`.
- `tasks/*.yml` — one file per group, included `flatten: true`: `task db:migrate`, never `task database:db:migrate`; add a group via `tasks/<group>.yml` + include in root `Taskfile.yml` (never tasks in the root file; `compose:*` is the exception). Default `task` runs `--list` alphabetically on purpose.
- Tool configs live in `.config/` — **except the two oxc configs** at repository root: they resolve `ignorePatterns` relative to their own directory, so `.config/` would silently break root-anchored patterns. `ncu` needs `--configFilePath .config --configFileName ncurc.json`.
- Root `--env-file` adds a dotenv file to the table the config file's directives resolve from — not a config layer, never a write target. Subcommands that write a file declare the flag separately.
- `app.config.json` is required and single source of truth, so resolution is **deferred**: `initConfig` stores result *and* failure on context; only a command that reads it reports it. Hard-failing in `Before` would break `config:generate` and `key:generate`.
- Never print a literal secret; `Redacted`/`RedactDSN`/`RedactKVURL` all read the one `secretKeys` list — a new secret is added there and nowhere else.
- Keep README lean (setup, tasks, certificates, deployment, license). "Implemented today" lists here, `.llms/architecture.md`, and the code are the source of truth; update them when a scaffold becomes real.
- Default local path requires only Postgres + local storage — never valkey, S3, or SMTP.
- Never rewrite a dotenv file without consent; destructive overwrite needs an explicit flag. Never run destructive database commands against a database holding state you did not create; E2E probe rows (users, sessions, enrollments) are cleaned up in the same turn they are created, and the server is stopped.
- `sb.Join` in go-sqlbuilder is an **INNER** join; a LEFT join needs `sb.JoinWithOption(sqlbuilder.LeftJoin, ...)`. A list that joins the account table with `Join` hides every record whose account was deleted — exactly the records `ON DELETE SET NULL` exists to keep. `SelectBuilder` has no `.Count()` — write literal `count(*)`. Single-use semantics (replay guards, code consumption) live in the UPDATE's WHERE clause; a zero `RowsAffected()` turns into `datastore.ErrNoRows`.
- `jwtutils.Caller.UserID` is the **wire form** (`user_…` TypeID), never the row's UUID — convert with `user.UUIDFromWire`. `uuid.Parse` on it fails at runtime as a 500.
- The RPC wire is snake_case JSON (the shared codec) — a field `totp_id` on the wire is `TotpId` in Go.
- Single-use/expiring state that minted rows race over must not carry a `UNIQUE(user_id)` unless one-per-account is the actual rule — several live bridges per account are legitimate; the expiry and consumption are the lifecycle.
- Yaak CLI: create with `yaak request create <ws> --json '{...}'`; update with `yaak update --json '{"id":"rq_…", ...}'` (id inside the payload — `request update <id> --json` is rejected). The E2E TOTP codes come from the `@hackpro/yaak-otp` plugin (`{[ otp.generate ]}` with the enrollment's Base32 secret).
- A nil feature service in an area's `Deps` is skipped silently by `features()` — the endpoint answers `unknown procedure`. The area's forwarding test must mount through the area's real `Package` + `Mount` over a container and assert every procedure is claimed; hand-built `Deps` passes while the wiring is broken.
- Health reports are published by an endpoint: generic check names, no DSN/URL/storage path; the CLI report may show paths (`StorageCheckWithTarget`).

## Committing

- Stage explicit paths only; verify with `git status`. Never `git add -A` / `git add .`.
- Message: `{feat,fix,docs,refactor,chore}[(scope)]: <concise message>`. Never push.
- Never commit without explicit instruction in that turn — finish work, recommend the one-line message, list changed paths.
- Never run: `git reset --hard`, `git checkout .`, `git clean -fd`, `git stash`, `git commit --no-verify`, `git push --force`.

## Related

- `.llms/architecture.md` — per-package design rationale and library decision records (agent-facing; `docs/` is for humans).
- `AGENTS.md` is the single instruction source for all agents (Codex, Elph, Copilot, Cursor, Gemini CLI).
