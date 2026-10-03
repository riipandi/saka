# AGENTS.md

One binary: HTTP/ConnectRPC API, embedded SPA, CLI. Rules live here. Per-package reasoning and traps live in `.llms/architecture.md` — read that section before changing a package. This file is the instruction source for every agent.

## Shape

- `cmd/` uses `urfave/cli/v3`. Run `task --list` for the commands that exist; each target wraps the CLI 1:1. `migrate:seed` and the smoke commands (`mailer:smoke`, `logger:smoke`, `otel:smoke`) register on debug builds only — the list still shows them. Anything the binary does not implement prints `not yet implemented`. Do not document, test, or treat a stub as working.
- SPA (React 19, TanStack, Vite) builds into `web/output/` and embeds. The Vite dev server proxies `/api`, `/rpc`, `/.well-known`, `/storage` to Go on `:3080`.
- Implemented: `database`, `internal/{audit,authz,config,datastore,guard,health,logger,cache,fetcher,mailer,queue,jobs,scheduler,storage,transport}`, `pkg/{crypto,envfile,jwtutils,responder,validate,testutils,printext}`, `api/connect`, `email/templates`, `web`, all of `modules/identity` (signin, session, signup, user, usergroup, authorization, verification, onetimeaccess, multifactor, password, jwks), `modules/auditlog`, `modules/apikey`, `modules/notification`, `modules/federation` (oidc client management + protocol, customclaim, scimsync), `modules/devicelogin`, `modules/storage` (bucket management). The rest of `internal/**` and `modules/**` is a scaffold.
- Pocket ID is passkey-only. Surfaces it never had are `[Saka]` in Yaak. Auth flows beyond ported endpoints follow Better Auth.
- Do not add, remove, or rename a top-level directory unless asked. Extend an existing package.
- Porting a plan into code means implementing it. A doc may describe a larger surface than the code has.

## Ambiguous decisions

Stop and confirm before writing code, migrations, protos, or config when scope, system design, or an architecture choice is unclear. Do not pick a design silently and ship it.

Ask when any of these is true:

- The request fits more than one package, area, table, or transport.
- A new surface, job, queue, config key, grant, or migration is implied but not named.
- `.llms/architecture.md` and this file leave more than one valid shape.
- The change would be hard to undo (schema, public contract, seed, authz rule, secret, storage key).
- You would have to invent a product rule (who can call it, what is stored, what is async).

Do not ask about rules this file already settles: stack, DI, SQL style, guard refusal codes, log frontend, goose layout, JSON v2, file size habits.

How to ask:

1. One line: what is unclear and what breaks if the wrong option ships.
2. A numbered list of concrete options (usually 2–4). Each option is a design, not a vibe.
3. Mark exactly one option `recommended` and give one sentence of why (constraint in this repo, blast radius, or fit with an existing package).
4. Always add a final free-input option so the human can name a different path in their own words.
5. Wait. Do not implement, generate, or migrate until they pick a number or write their own.

Template:

```text
Unclear: <decision> — wrong pick means <cost>.

1. <option> — <one-line consequence>
2. <option> — <one-line consequence>  [recommended: <one reason>]
3. <option> — <one-line consequence>
4. Other — reply with the design you want
```

Must-ask examples: new area vs extend an existing package; new table vs reuse; sync handler vs queue vs job; a guard rule the proto cannot express; dual-write or a compatibility shim (forbidden unless they override); a config key that is not yet in `types.go` / schema / `secretKeys`.

If they already chose in the same thread, do not re-ask. If new facts change the choice, confirm the delta only.

## Stack

- Go 1.27.1, Node >= 24.21, pnpm 12.6.0, Docker (testcontainers), `task`. chi, pgx, optional Valkey, `samber/do`, goose as a library, koanf, LogLayer behind `log/slog`, OpenTelemetry, S3 and local storage.
- Cache defaults to memory. Session store and rate limit default to Postgres (`public.rate_limits`, `fn_check_rate_limit` under advisory locks). A default local run needs Postgres and local storage only.
- Feature code logs through `log/slog`. LogLayer is the handler (`integrations/sloghandler`), never called from a feature.
- DI is `do.Package` values in `internal/registry`. No global singletons, no package `init`. Use skill `golang-samber-do`. `infrastructure.go` names no module. `modules.go` holds only the `Area` seam (`{Name, Package, Mount}`). Each area wires itself in `modules/<area>/module.go`. `registry.New(..., extra ...Area)` serves a consumer area without editing the registry.
- A service another area or the transport resolves is registered in `infrastructure.go`. An area `Package` holds only what its own features consume. Two areas sharing a service is the signal to promote it. `do.Scope` stays unused until an area must hide a service.
- A provider only constructs: no database I/O, no seeding, no network dial beyond the dependency it resolves. Work that touches state is its own service or runner, so the prewarm walk orders it and its failure fails the run.

## Tasks

- Run `task --list` for targets and descriptions. A target wraps the CLI 1:1 (`db:migrate` → `migrate:up`). Renaming a command breaks the task with no compile error. Extra flags go after `--`.
- E2E probes hit `build/release/saka serve --env-file=.env.local`. A green unit suite does not exercise the composition root.
- Reset a dev database you created with `task db:reset -- --force --up`, then `task db:seed -- --force`. Do not hand-drop tables. Do not reset a database that holds state you did not create.
- `task rpc:generate` rewrites Go and TypeScript from `api/connect/*.proto`. `task rpc:stale` fails when the contract moved and the generated code did not.
- `task cert:generate` / `cert:trust` write `storage/config/localhost_{key,crt}.pem` (mkcert, else openssl). nginx in `container/compose-dev.yaml` reads them.
- Integration tests call `testutils.SkipWithoutDocker(t)` first, then `StartPostgres` / `StartMailpit` / `StartMinIO` / `StartValkey` / `StartVictoriaLogs`. A missing daemon skips. Do not widen a timeout to hide a hung container.
- Local Postgres: `docker compose -f container/compose.yaml up -d pgsql`. `task metrics:up` starts the observability half. Full stack and observability share that compose file.

## Contracts

Schema truth is goose SQL in `database/`. Do not embed DDL or create tables at runtime. `migrator.go` runs goose v3 on one pinned connection. `validate.go` checks with no DSN. `create.go` writes a skeleton. `dump.go`, `backup.go`, `schema_dump.go`, and `restore.go` use COPY text, primary-key order, idempotent replay. `compress.go` is `none|gzip|zlib|zip` with byte-level detection. Seeders are idempotent; the caller owns the transaction.

An area module lists features, builds one router each, and takes a `Deps` struct of resolved services. The area owns `Package` and `Mount`. The registry does not learn feature internals. `schema.go` names columns where the query is (`sqlbuilder.NewStruct` for full CRUD), not one constant per column.

- **Multifactor** (`saka.authn.v1.MultifactorService`): up to 10 devices, `totp_` TypeIDs, secret sealed with the shared `enc:` cipher. Password success mints a 5-minute pending bridge (hashed; several may be live). `CompleteSignIn` spends it with a TOTP or recovery code. One recovery set per account, 10 hashed single-use codes, shown once. Replay is `last_used_step` compare-and-set. Three wrong codes end a bridge. Disable, regenerate, and delete-last require a second-factor proof. The sign-in fork is the `mfaGate` interface, wired after construction.
- **JWKS** at `GET /.well-known/jwks.json`: the database is the one signing authority — active `public.jwks` signing rows only (private halves sealed `enc:` with the auth cipher from `AUTH_SECRET_KEY`). `saka initialize` provisions the first pair (seeder + `jwks_provisioned` audit event); `jwks:generate` stages a rotation pair; the environment carries no key-pair material. Each row carries `seal_fp`; an `AUTH_SECRET_KEY` rotation retires stale-seal rows and re-provisions on the next signing read (`jwks_invalidated` audit event). Rotating `APP_SECRET_KEY` never touches the signing keys. `*jwks.Service` is the `jwtutils.KeyProvider`. Signing is dual-stack (database key pair and HMAC `auth.secret_key`); `auth.jwt_algorithm` chooses only when both are set, and is omitted from the sample — otherwise the active row's `algorithm` column or the secret's length decides. Bare RFC 7517 set, not an envelope. The symmetric key is never published. A module with no provider fails closed.
- **Notification** (`saka.notification.v1.NotificationService`): audiences `global`, `users`, `user_groups`, resolved by query, never fanned out at create. Read state is one receipt per account. `system` notices target accounts and carry neither a topic nor a global audience. Announcements default to global, topic optional. `WatchNotifications` is an in-process broker with ping keepalive and no history (catch-up is List); `middleware.UnboundedFor` lifts its deadline. Email is an `EmailDispatcher` over the `notification_email` queue, gated by `mailer.notifications.announcement_email_enabled`. The processor in `internal/jobs` reads the audience through this package's repository.
- **Authz.** A handler never checks a role. `jwtutils.CallerFrom` returns subject, claims, and `ActorID` / `ActorUsername`. `IsImpersonating()` refuses a delegated caller on `Self` and admits one on `Admin` and `Authenticated`. Permissions are code-declared `resource:id:action` (`*` only in the instance position). The catalog is seeded and read-only. A role is a named set. Grants are the account's roles plus its direct grants (`public.roles`, `permissions`, `role_permissions`, `user_roles`, `user_permissions`, migration `00003`). `user.LoadGrants` is the only door every mint uses. The access token carries `roles` and `permissions`; a grant change lands at the next mint or refresh. `users.is_admin` is gone. `guard.Admin` reads the `administrator` system role. The seeder grants it to the default account. `saka initialize` bootstraps a fresh database.
- **Guard.** `ProcedureRules` and `RestRules` feed the RPC interceptor and the REST bearer middleware. A request the tables do not name is administrative. Rules: `Public`, `Authenticated`, `Self(field)`, `Session`, `StopImpersonating`, `Admin`, `Permission(slug)`. A refusal is `unauthenticated` (401) or `not_found` (404), never `permission_denied`.
- **Audit.** Event names, `Entry`, `ClientInfo`, nil-safe `Recorder` live in `internal/audit`. `modules/auditlog` is the reader — the split is required, because the reader imports `modules/identity/user` and the transport imports no module. `Record(ctx, db, entry)` runs on the query surface of the change, so pass `tx`. Client facts and the impersonation actor come from the context. After a user delete, `user_id` is `ON DELETE SET NULL`, so the record names the account in `resource_type` / `resource_id`. Retention is `audit.retention_days` plus the `audit_cleanup` job.
- **Datastore.** One `pgxpool`. `Querier` / `WithTx`. Migrations use `OpenMigrationDB`, not the pool. Startup retries `database.connect_attempts` × `database.connect_retry_interval`. `PostgresOptions.ConnectAttempts` defaults to 1, so the wait is something the caller asked for. SQLSTATE `28P01`, `28000`, or `3D000` ends the wait immediately. A cancelled context ends it too.
- **Storage.** Buckets are the namespace: `storage_buckets` + `storage_objects` (migration `00009`), the file stored whole under `{bucket}/{key}` (local `storage.local_path/{bucket}/{key}` or the S3 prefix). There is no chunk store. The upload task is `StorageUploadTask`. `Stage` writes staging and the intent row, and **the caller enqueues the upload when it returns** — there is no watcher; the boot-time sweep (`jobs.SweepUploads`, the `upload sweep` runner) re-enqueues the staged rows a dead run left. `Sync` hashes and uploads. `BeforeSyncHook` / `AfterSyncHook` are nil until the registry wires them, and must be idempotent; `AfterSyncHook` enqueues `upload_finished_notice` for the metadata `owner`.
- **tus.** The resumable-upload protocol (1.0.0, in-house — the library record is in `.llms/architecture.md`) lives in `internal/storage/tus*.go` and mounts at `/api/uploads`: `OPTIONS` discovery (guard-Public), `POST` creation with the `Upload-Metadata` naming bucket and key (authenticated), `HEAD` offset, `PATCH` chunk, `DELETE` termination. The final chunk's completion enqueues the upload itself; `storage_upload_expiry` reclaims interrupted sessions after `TusSessionExpiry` (24 h). There is no progress route: the HEAD is the offset read. Any signed-in account may upload into any bucket.
- **Serving.** Files are served at `GET /storage/{bucket}/{key}` (module `internal/transport/storage`), outside the throttled group, before the SPA. A missing bucket or object is 404, never `index.html`. The handler never lists a directory. `staging/`, `logs/`, `backup/`, `config/`, and `files/` under the data directory are not served. A feature builds its URL as `app.assets_url + /{bucket}/{key}` (`app.assets_url` defaults to `/storage`).
- **Bucket management.** `modules/storage` (`saka.storage.v1.BucketService`, feature `bucket`) is the Admin-gated CRUD over `storage_buckets`, with audit events `storage_bucket_*` and the `storage.default_bucket` appconfig setting. Deletion refuses a non-empty bucket and the setting-named one. Wire ids: `bkt_` (bucket), `file_` (object).
- **Other internals.** Cache: one `Cache`; Noop when caching is off; Valkey keys use `saka:cache:`. Fetcher: Resty, typed errors, bodies and credentials never logged. Health names are `database`, `kvstore`, `storage`; published reports carry no DSN, URL, or path (the CLI may, via `StorageCheckWithTarget`). Logger: `Slog()` is the only frontend; sinks `console|file|otlp`. Mailer: empty host refuses; the body streams into `DATA`; plaintext auth off loopback needs `mailer.smtp_allow_plaintext_auth`. Observer: exporter options are explicit, no environment fallback; Prometheus at `otel.metrics.prometheus_path`. Queue is SKIP LOCKED with optional payload encryption. Jobs are concrete processors; recurring ones re-enqueue. Scheduler claims a cron tick with a row lock and enqueues; the queue runs it. `pkg/crypto`: AES-256-GCM `enc:`, PHC hasher, `KeyGenerator`, `DecodeJWK`, `ParseHMACKeyHex` (not `ParseKeyHex`). Do not add a format. `pkg/printext`: `Style` is meaning, `Palette` is colour. `pkg/envfile` preserves comments and key order. `pkg/responder` and `pkg/validate` are the only envelope and validation paths. `pkg/jwtutils` holds typed claims. `pkg/testutils` holds the containers. Kernel, transport, and registry contracts are in `.llms/architecture.md`.

## How to change it

- Confirm first when the change is a scope, system-design, or architecture fork. See **Ambiguous decisions**. Implementation detail that this file already names does not need a question.
- **Config** resolves only in `internal/config`: defaults, then the JSON file, then CLI args. Commands read `configFrom` / `fullConfigFrom`. They do not read `os.Getenv` or flags. Environment is a value table the file references (`${NAME}` inline, `env:NAME` as a whole value). Only `flagBindings` become flags, and only `serve` has them. A flag that changes what a command does (`--dry-run`, `--force`) is never a binding. Root `--env-file` adds a dotenv file to that table. It is not a layer and not a write target. Subcommands that write a file declare the flag themselves. `app.config.json` is required. `initConfig` stores success and failure on the context. A command that reads config reports the failure. Failing in `Before` would break `config:generate` and `key:generate`.
- Every new config key has a `config.Default`, a `config.Validate` rule, and an entry in the generated `config.schema.json` (written by `config:generate`; the file is gitignored). A secret is listed in `secretKeys` and nowhere else. `Redacted`, `RedactDSN`, and `RedactKVURL` read that list. `.env.example` carries secrets and deployment variables only. Config lives in one file per stage: `types.go`, `values.go`, `defaults.go`, `keys.go`, `file.go`, `env.go`, `flags.go`, `validate.go`, `predicates.go`, `redact.go`, `config.go`. Do not add a file for one function.
- **SQL** against application tables uses `go-sqlbuilder`, PostgreSQL flavor, as in `internal/queue/store.go`. Raw SQL is for tests and `database/` maintenance. A `SELECT` of a function uses `sb.Var`, never hand-written `$1`. `sb.Join` is an inner join. A left join is `sb.JoinWithOption(sqlbuilder.LeftJoin, ...)` — an inner join to `users` hides rows kept by `ON DELETE SET NULL`. There is no `SelectBuilder.Count`; write `count(*)`. Single-use state (replay, code consumption) belongs in the `UPDATE`'s `WHERE`. Zero `RowsAffected` becomes `datastore.ErrNoRows`.
- **Go.** Production JSON is `encoding/json/v2` with `omitzero`. `encoding/json` is test-only. Check <https://go.dev/doc/jsonv2-migration> and test a delta a call site can hit. `DefaultOptionsV1` needs a justifying comment. Also: `for range n`, `t.Context()`, `errors.Join`, `slices` / `maps`, `min` / `max`, stdlib `uuid`. Flattened struct literals (proposal #9859): promoted fields are literal keys. Use an explicit nest only when names collide or the embedded name is the domain label. Do not mix the two forms. Migrate with `go fix -embedlit ./...`.
- Shutdown is context cancellation. Long-running components stop and drain. No compatibility shims, fallback readers, or dual-write paths. Fix lint at the source. `//nolint` is not accepted.
- Command output uses `printext.Plural` and `printext.Duration`. Item lines are indented. Outcome lines sit at column zero under `status:` so `grep '^status:'` works. Print a `database:` target line before touching anything. A parseable command prints only its value. Colour comes from `pkg/printext`, never for `--json`.
- Keep file size and folder depth in balance. Split unrelated concerns. Do not add a package that holds one function, and do not nest for its own sake.
- **Module.** `modules/<area>/<feature>/` with `schema.go`, `repository.go`, `service.go`, `handler_rpc.go`, plus `handler.go` for REST and `module.go`. Implement the kernel contract. List it in the area's `features()` — that is the only registration an existing area needs. `Deps` carries resolved services, never `internal/registry`. A new area also exports `Package` and `Mount` and adds one `registry.Areas()` entry. Business logic is in `service.go`. Handlers map transport only. One pool, `internal/datastore`. IDs are `go.jetify.com/typeid`. Token and code rows store a hash plus `uuidv7()`.
- **Endpoint.** Constraints go on the proto as `buf.validate.field`. `task rpc:generate`. Implement, mount, and declare the rule in `internal/guard`. An undeclared public or self-service procedure answers `not_found` until it is declared. A rule the contract cannot express stays in the feature. Do not re-check a contract constraint in Go. Update the Yaak request in the same change and drop `(unimplemented)` when it ships. Descriptions stay aligned with `.llms/references/pocket-id-api-reference.md`. Probe a freshly built binary before calling it served.
- **RPC probing.** Test RPC calls with `scripts/curl-rpc.sh`, not hand-rolled curl: `./curl-rpc.sh http://localhost:3080/rpc/saka.authn.v1.AuthService/SignIn '{"identity":"admin","password":"@dmin123"}'`. The URL is the full procedure path, `/rpc` prefix included; the JSON body is one argument; extra curl args go after it (`-s -H "Authorization: Bearer …"`). The banner goes to stderr, so stdout is the response body alone — pipe to `jq` or `python3` directly. REST endpoints keep plain curl (the wrapper speaks the ConnectRPC POST shape).
- **Migration.** `task db:create -- add_widgets`. `-- +goose Up` and `Down` are mandatory. Versions are five digits, consecutive, above the last. Goose skips `00000_*.sql` silently; `migrate:validate` and the migrator tests catch that file. Rebuild before an embedded migration runs. Tests derive counts from `database.EmbeddedMigrations()`.
- **Seeder.** Factory in `database/seeders/`, appended to `All()` in dependency order, named `<Entity>Seeder`, `ON CONFLICT DO NOTHING`, existing rows reported skipped. `Apply` receives a `datastore.Querier`. The command owns the transaction.
- **Dump object.** One writer in `dumpDDL`'s section list (`database/schema_dump.go`), then restore order. Prefer server deparse. Skip `pg_depend.deptype = 'e'`. Read whole lists before per-item queries. Add a round trip in `backup_test.go`.
- **Schema test.** `testutils.StartPostgres(t).NewDatabase(t)` per test. Call `NewDatabase` again for a second database. Migrated tables: `OpenMigrationDB`, migrator `Up`, close, then open the pool.
- **Job.** Task type, `QueueConfig`, processor in `internal/jobs/<name>_job.go`, register in `internal/jobs/register.go`. A recurring job re-enqueues.
- **Audit event.** Name it in `internal/audit/audit.go` (snake_case, what happened). `s.audit.Record(ctx, tx, audit.Entry{...})` inside the causing transaction. Do not pass the pool, or the log can describe a rollback. Do not thread address, User-Agent, fingerprint, or the impersonation actor through the service.
- **Cross-feature gate.** Define the interface in the consuming package and wire it after construction in the area `Package` (`users.WithBanSideEffects(...)`, `issuer.WithMFAGate(...)`). A provider that took the dependency would cycle. Do not import the feature package back. Sealed storage has two halves: `crypto.NewAuthCipher(c.Auth.SecretKey)` for auth material (JWKS private keys, multifactor TOTP secrets — a rotation of `AUTH_SECRET_KEY` is what invalidates them) and `crypto.NewCipherFromHex(c.App.SecretKey)` for everything else (appconfig, SCIM tokens, queue payloads). A nil cipher is answered at the call site, not by failing the run.
- **Log sink.** Name it in `LogTransport*`, default and `Validate`, build in `internal/logger`, hold resources for `Shutdown`, and add a case to `TestEveryTransportShipsTheSameEntry`.
- **Mail.** Render and submit in one `*mailer.Service` call. A new template is a `.tsx` under `email/templates/` plus a struct in `internal/mailer/data.go` matching `TemplateProps`. Prove it with Mailpit (`docker compose -f container/compose.yaml up -d mailpit`, `task mailer:smoke -- --to=you@example.com`, <http://localhost:8025>). `internal/mailer/integration_test.go` covers the same path without compose.
- **OTel.** Shared address and identity live under `otel`. Signal-specific settings live in that signal's subsection. There is no second collector address. Exporters speak `http/protobuf` only — there is no `otel.protocol` key and no gRPC exporter; the endpoint is the URL the exporter dials. A deployment-set value is in `envKeys`. After any `go get`, re-pin `go.opentelemetry.io/otel/log`, `otel/sdk/log`, and the otlplog http exporter to v0.19.0 (`go mod edit -require`). LogLayer's otellog transport does not compile above `otel/log` v0.19.0. Core `otel/sdk` v1.46.0 is a separate line.
- Prove logs: `task metrics:up`, then `LOG_TRANSPORT=console,file,otlp task metrics:smoke`, then `task metrics:query -- "SELECT marker, count(*) AS n FROM default GROUP BY marker LIMIT 10"`. Prove traces and metrics: `OTEL_TRACING_ENABLE=true OTEL_METRICS_ENABLE=true task metrics:smoke:otel`, then `task metrics:traces` and the `saka_otel_smoke_total` query (`metrics:query -- "SELECT * FROM default_metrics ..."` needs `otel.metrics.push=true`; pull mode reads the app's own `/metrics`). The stack is OpenObserve (:5080) behind the OTel collector contrib (:4318); Basic auth is `email:password` from compose-sre.yaml, search timestamps are microseconds inside the `query` object.

## Library docs

Always use the MCP Context7 and DeepWiki tools to look up library documentation/references (instead of relying on memory or source-reading alone) when working with external libraries.
The pinned source of truth is the module cache (`go env GOMODCACHE`). When docs and code disagree, code wins — say so. Settled comparisons go in `.llms/architecture.md` under "Library decision records".

## Traps

- `api/specs/` is the Yaak sync mirror. The sync writes it. Change requests with the `yaak` CLI. The collection is a checklist. The code and `.llms/references/pocket-id-api-reference.md` are the reference. A ported endpoint may change shape. Names: `[Pocket ID]` for an upstream summary, `[Saka]` for a saka-only surface. Create with `yaak request create <ws> --json '{...}'`. Update with `yaak update --json '{"id":"rq_…", ...}'` — the id is inside the payload. E2E TOTP codes come from `@hackpro/yaak-otp` (`{[ otp.generate ]}` and the enrollment's Base32 secret).
- `codegen/` is gitignored. Regenerate with `task rpc:generate`. Do not edit it.
- `scripts/task-*.yml` is one file per group, included `flatten: true`. Run `task db:migrate`, never `task database:db:migrate`. Add a group as `scripts/task-<group>.yml` plus an include in the root `Taskfile.yml`. The root file holds no task body. `compose:*` is the exception. Default `task` lists targets alphabetically on purpose.
- Tool configs live in `.config/`, except the two oxc configs at the repository root. They resolve `ignorePatterns` from their own directory. `ncu` needs `--configFilePath .config --configFileName ncurc.json`.
- README stays lean: setup, tasks, certificates, deployment, license. "Implemented today" is this file, `.llms/architecture.md`, and the code. Update them when a scaffold becomes real.
- Do not rewrite a dotenv file without consent. A destructive overwrite needs an explicit flag. E2E rows (users, sessions, enrollments) are removed in the same turn they are created, and the server is stopped.
- `jwtutils.Caller.UserID` is the wire form (`user_…`). Convert with `user.UUIDFromWire`. `uuid.Parse` on the wire form fails at runtime as a 500. RPC JSON is snake_case: proto `totp_id` is Go `TotpId`.
- Single-use or expiring rows that concurrent mints race over must not have `UNIQUE(user_id)` unless one-per-account is the rule. Several live bridges per account are legitimate.
- A nil feature service in `Deps` is skipped by `features()`. The procedure answers `unknown`. The area's forwarding test mounts the real `Package` and `Mount` over a container and asserts every procedure is claimed. A hand-built `Deps` passes while the wiring is broken.
- Do not print a secret. See the config rule for `secretKeys`.

## Prose

- Documents checked into the tree are English: `.llms/` (architecture, audits, handovers), `docs/`, and the rest. `.llms/` is for agents. `docs/` is product documentation for humans. Chat may follow the user's language. Files do not.
- Before writing or rewriting that prose, use skill `clarity`. An existing draft is rewrite mode. A new document still carries only claims the tree or the user supplied. Skill `handoff` picks the handover filename and sections. `clarity` is the pass over the sentences. Keep paths, counts, conditions, and rejected alternatives. A reference stays scannable. It does not become an essay.

### Plan documents

A plan (`.llms/plans/plan-<date>_<time>.md`) is a contract with one fixed shape. A plan missing any part is not ready for approval — the owner rejects it rather than repairs it. The mandatory sections, in order:

1. YAML frontmatter: `status` (`awaiting-approval` until the owner says go; the phases' own statuses carry progress; `shipped`/`done` when everything lands), `updated` (last revision, timestamp with UTC offset), and `phases` (one entry per phase: `id`, `title`, `status`, `note` — the decisions and corrections the phase settled; `commits` once they land).
2. Objective — what the work closes, naming the design-doc rows.
3. Current state — every claim verified in the code with file and line, never taken from a document. A config key or a doc checkbox is not evidence of an implementation (the HIBP lesson: the key existed, the feature was shipped, the docs said otherwise).
4. Decisions — dated, binding, each carrying its rejected alternatives.
5. Shape and enforcement points — tables, keys, seams, and the surfaces each change lands on.
6. Execution rules — the binding block: atomic commit per phase is authorized, each phase's heading names its suggested commit message, the standing commit rule resumes when the plan completes, migration and dev-DB-reset authorization, Yaak updates ride the same change as the contract, per-phase validation runs before each commit.
7. Phases — numbered, each with a validation line.
8. Test matrix — what is validated, how, and what counts as green (unit, container, wire-level probes, lint, `rpc:stale`).
9. Explicitly out of scope — the owner-settled refusals, so they are not re-proposed.

Hard rules:

- The frontmatter and the body never disagree. Any status or phase update moves both in the same commit. A body that still reads as future work while the frontmatter says landed/shipped is a defect to fix on sight — not a note for later.
- Enumerate the full surface the plan ships: every procedure, REST route, job, email, audit event, guard rule, settings key, and each one's Yaak row. A shipped surface without its Yaak row is an incomplete phase; so is an email with no trigger.
- Name the flow reference (the product and the docs' read date) and follow its flow as the primary source. Deltas from it are decisions with recorded reasons, not silent deviations. An agent recommendation the owner accepts that contradicts the reference is also a recorded delta — re-check it against the reference before writing it in.
- A plan is executed only on the owner's explicit instruction: never start, resume, or continue automatically, and never run one because it exists. Once running, each independently validated phase commits as it lands — do not wait for the whole plan — and each commit is limited to that phase's files.
- When the phases have all landed, the agent does not commit the closing state on its own: it stops, reports the outcome, and recommends the commit message (with the paths) for the owner to approve. The same holds for any change a plan did not pre-authorize.
- The closing phase owns the docs sync — architecture, endpoint reference, api docs, auth-design checkboxes, the Yaak collection, and the handover document. A design-doc checkbox left unchecked over shipped code is a defect of that phase.
- Comments say what the code cannot: invariants, security rules, protocol requirements, side effects. Do not restate code, narrate control flow, or repeat a name. No phase markers (`phase *`, `Step N`, `TODO(plan)`). A real rework gets a `TODO` or `FIXME` that says what and why. Do not write "owned by" — describe the invariant. One point per comment. If half the words still carry it, it was too long.
- Test fixtures (usernames, emails, display names, tokens, placeholder text) come from Dan Brown (Robert Langdon, Sophie Neveu, Vittoria Vetra, Inferno, Digital Fortress) and Harry Potter (Hermione Granger, Hogwarts, Gryffindor, Horcrux, Expecto Patronum). Ordinal signup tokens follow the book order (`philosophers-stone`, `chamber-of-secrets`, …). Test names stay behavioral (`TestSignupRejectsADuplicateAccount`).

## Git

- Stage explicit paths. Check `git status`. Never `git add -A` or `git add .`.
- Message: `{feat,fix,docs,refactor,chore}[(scope)]: <concise message>`. Do not push.
- Do not commit unless this turn asked for it. When the work is finished, recommend the one-line message and list the paths.
- Never run `git reset --hard`, `git checkout .`, `git clean -fd`, `git stash`, `git commit --no-verify`, or `git push --force`.
