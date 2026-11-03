# The repository shape

The monorepo, the toolchain, the frontend conventions, and the porting
contract — the tech detail `AGENTS.md` points here for. Per-package
reasoning stays in `architecture.md`; per-package contracts stay in
`rules.md`. Verified against the tree 2026-10-14.

## The layout

- **One binary** — an HTTP + ConnectRPC API, an embedded SPA, and a CLI
  in one Go process. `cmd/` is the CLI entry (`urfave/cli/v3`);
  `task --list` mirrors its commands.
- **The backend tree**: `internal/` (framework wiring the application
  owns — config, registry, jobs, transport), `modules/` (the area
  modules — one package per bounded area, each with `Package` and
  `Mount`), `framework/` (the extracted, application-agnostic packages
  — config engine, queue, scheduler, storage, mailer, audit, health,
  fetcher, datastore), `pkg/` (the libraries any Go code may import).
  `internal/registry` is the only composition root; kernel, transport,
  and registry contracts are in `architecture.md`.
- **The frontend is a pnpm + Vite+ monorepo** of independent packages
  under `packages/`: `webapp` (the React 19 + TanStack SPA source, plus
  `public/` — its `vite.config.ts` owns the SPA pipeline and the Go
  binary targets), `uilibs` (the presentational component library —
  components, styles, theme, hooks; no data fetching, no build target
  of its own, compiled by the consumer; Storybook stories beside the
  components), `email` (React Email templates — its own `build.ts`
  compiles them into the Go embed, content-hash cached), `plugins`
  (the shared Vite plugin: golang), `e2e-tests` (Playwright).
- The root `vite.config.ts` is monorepo setup only (staged/fmt/lint/
  typecheck), with no app pipeline; the Taskfile sequences the passes
  (`task build` = email then webapp, `task dev` = the webapp dev loop).
- The Go shell (`web/shell.go`) owns the HTML document — there is no
  `index.html`. The dev server proxies `/api`, `/rpc`, `/.well-known`,
  and `/storage` to Go on `:3000`.
- **The stack**: Go (pin in `go.mod`), Node ≥ the engines field, pnpm,
  Docker (testcontainers for tests), `task`; chi, pgx, optional Valkey,
  `samber/do` DI, goose as a library, koanf, LogLayer behind
  `log/slog`, OpenTelemetry, local and S3 storage. The dependency and
  DI rules (`do.Package`, no singletons, no package `init`) are in
  `rules.md` and the skill `golang-samber-do`.

## The Vite+ toolchain

The frontend toolchain is Vite+ (`vp`): one CLI over Vite/Rolldown,
Vitest, Oxlint, Oxfmt, and its task runner. Docs:
`node_modules/vite-plus/docs` or <https://viteplus.dev/guide/>.

- `vp <name>` runs a built-in; `vp run <name>` runs a `package.json`
  script or a `vite.config.ts` task — they may differ. Check both
  before assuming what a name runs.
- `vp check` = format + lint + type check (`--fix` to write);
  `vp fmt` / `vp lint` are the pieces. The lint/fmt settings live in
  the root `vite.config.ts` (migrated from the former `.oxlintrc.json`
  / `.oxfmtrc.json`).
- `vp toolchain [tool]` shows versions; `vp why <package>` the
  dependency graph. Run `vp install` after pulling remote changes.
- **Commit hooks.** The dispatcher is `core.hooksPath → .vite-hooks/_`
  (generated; `prepare: vp config` reinstalls it). The project-owned
  `.vite-hooks/pre-commit` runs `vp staged` — the `staged` block in the
  root `vite.config.ts` maps `*.{ts,tsx,js,jsx,css,json}` to
  `vp check --fix` and `*.go` to `gofmt -w`, re-staging fixed files —
  then guards the request-spec export with `scripts/check-yaak-secrets.sh`
  and the typecheck chain with `scripts/check-typecheck-parity.sh`.
  `VP_GIT_HOOKS=0` skips hooks per process.

## The frontend conventions

- Compose screens from `packages/uilibs/src/components/` — the library
  is comprehensive; pick the component that fits the item (Item for
  list rows, Table for grids, AlertDialog for confirmations, …) and
  never reshape an existing uilibs design to fit a feature. Check the
  component's Storybook story beside it before building on it.
- Route files stay thin (the full contract is `rules.md`'s webapp
  entry): a route file declares `createFileRoute` with
  `component: RouteComponent` — a thin wrapper wiring
  `useSearch`/`useNavigate` — while the implementation lives beside it
  in a dash-prefixed file (`-settings-view.tsx`); dash files are
  excluded from the route tree and never render as URLs. Never mount a
  dash-file view as the route's component directly. Colocated files
  import each other with relative paths; the `#/` alias is for
  cross-directory reaches. See
  <https://tanstack.com/router/latest/docs/routing/file-based-routing>.
- Style rules: `.llms/stylex-authoring.md` (read before writing
  styles), setup in `.llms/stylex-installation.md`; the component
  inventory is `.llms/ui-components-index.md`.

## Static analysis

`task analysis:deadcode` runs `go tool deadcode ./...` — the
unreachable-function report over the non-test build. The tool rides
go.mod's `tool` directive; `go get -tool golang.org/x/tools/cmd/deadcode@latest`
adds or updates it (the pin is committed, so the report is reproducible
across machines).

## The porting contract

The product is a port of an upstream codebase (Pocket ID): Pocket ID is
passkey-only — surfaces it never had are `[Saka]` in the Yaak request
collection; auth flows beyond the ported endpoints follow Better Auth's
models. The intentional departures are every line of
`.llms/upstream-deviations.md`.

## The working rules this shape implies

- Implemented surfaces are listed in `index.md` and `architecture.md`;
  the rest of `internal/**` and `modules/**` is a scaffold. Do not
  document, test, or treat a stub as working.
- Do not add, remove, or rename a top-level directory unless asked.
  Extend an existing package.
- Porting a plan into code means implementing it. A doc may describe a
  larger surface than the code has.
- No self-evident comments: a comment must carry what the code cannot
  (a why, a contract, a trap), never restate what the next line already
  says.
