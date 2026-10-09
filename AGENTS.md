# Agents Instructions

One binary: HTTP/ConnectRPC API, embedded SPA, CLI. This file holds the general
instructions; the detail lives in `.llms/`. Read in this order:

1. This file — behavioral rules, always in force.
2. `.llms/index.md` — the documentation map.
3. `.llms/rules.md` — the detailed rules: tasks, per-package contracts, how-to-change recipes, traps. Read the section a change touches before touching it.
4. `.llms/architecture.md` — per-package reasoning and library decision records. Read that package's section before changing it.

## Shape

- `cmd/` uses `urfave/cli/v3`; `task --list` mirrors the CLI. The frontend is a pnpm + Vite+ monorepo of independent packages under `packages/`: `webapp` (the React 19 + TanStack SPA source, plus `public/` — its `vite.config.ts` owns the SPA pipeline and the Go binary targets), `email` (React Email templates — its own `build.ts` script compiles them into the Go embed, content-hash cached), `plugins` (the shared Vite plugin: golang), `e2e-tests` (Playwright). The root `vite.config.ts` is monorepo setup only (staged/fmt/lint/typecheck), with no app pipeline; the Taskfile sequences the passes (`task build` = email then webapp, `task dev` = webapp dev loop). The Go shell (`web/shell.go`) owns the HTML document, so there is no `index.html`. The dev server proxies `/api`, `/rpc`, `/.well-known`, `/storage` to Go on `:3000`.
- Implemented surfaces are listed in `.llms/index.md` and `.llms/architecture.md`; the rest of `internal/**` and `modules/**` is a scaffold. Do not document, test, or treat a stub as working.
- Pocket ID is passkey-only. Surfaces it never had are `[Saka]` in Yaak. Auth flows beyond ported endpoints follow Better Auth.
- Do not add, remove, or rename a top-level directory unless asked. Extend an existing package.
- Porting a plan into code means implementing it. A doc may describe a larger surface than the code has.
- No self-evident comments: a comment must carry what the code cannot (a why, a contract, a trap), never restate what the next line already says.
 
## Frontend UI

- Compose screens from `packages/uilibs/src/components/` — the library is comprehensive; pick the component that fits the item (Item for list rows, Table for grids, AlertDialog for confirmations, …) and never reshape an existing uilibs design to fit a feature. Check the component's Storybook story beside it before building on it.
- Apply the skills `emil-design-eng` and `apple-design` when composing or implementing UI.
- Keep route files small with TanStack Router code splitting: a route file declares `createFileRoute` with `component: RouteComponent` — a thin wrapper defined in the same file that wires `useSearch`/`useNavigate` to the view — while the implementation lives beside it in a dash-prefixed file (`-settings-view.tsx`, `-sessions-view.tsx`) — dash-prefixed files are excluded from the route tree and never render as URLs. Never mount a dash-file view as the route's component directly. See <https://tanstack.com/router/latest/docs/routing/file-based-routing>.
- Import colocated files (the dash files beside a route) with relative paths (`./-settings-dialog`), not the `#/` alias — the alias is for cross-directory reaches.

## Stack (summary)

Go 1.27.1, Node ≥ 24.21, pnpm, Docker (testcontainers), `task`. chi, pgx, optional Valkey, `samber/do` DI, goose as a library, koanf, LogLayer behind `log/slog`, OpenTelemetry, local and S3 storage. DI rules (`do.Package`, no singletons, no package `init`) and the registry seams are in `.llms/rules.md` and the skill `golang-samber-do`.

## Vite+ toolchain

The frontend toolchain is Vite+ (`vp`): one CLI over Vite/Rolldown, Vitest, Oxlint, Oxfmt, and its task runner. Docs: `node_modules/vite-plus/docs` or <https://viteplus.dev/guide/>.

- `vp <name>` runs a built-in; `vp run <name>` runs a `package.json` script or a `vite.config.ts` task — they may differ. Check both before assuming what a name runs.
- `vp check` = format + lint + type check (`--fix` to write); `vp fmt` / `vp lint` are the pieces. The lint/fmt settings live in the root `vite.config.ts` (migrated from the former `.oxlintrc.json` / `.oxfmtrc.json`).
- `vp toolchain [tool]` shows versions; `vp why <package>` the dependency graph. Run `vp install` after pulling remote changes.
- **Commit hooks.** The dispatcher is `core.hooksPath → .vite-hooks/_` (generated; `prepare: vp config` reinstalls it). The project-owned `.vite-hooks/pre-commit` runs `vp staged` — the `staged` block in the root `vite.config.ts` maps `*.{ts,tsx,js,jsx,css,json}` to `vp check --fix` and `*.go` to `gofmt -w`, re-staging fixed files — then guards the Yaak export with `scripts/check-yaak-secrets.sh`. `VP_GIT_HOOKS=0` skips hooks per process.

## Ambiguous decisions

Stop and confirm before writing code, migrations, protos, or config when scope, system design, or an architecture choice is unclear. Do not pick a design silently and ship it.

Ask when any of these is true:

- The request fits more than one package, area, table, or transport.
- A new surface, job, queue, config key, grant, or migration is implied but not named.
- `.llms/architecture.md` and this file leave more than one valid shape.
- The change would be hard to undo (schema, public contract, seed, authz rule, secret, storage key).
- You would have to invent a product rule (who can call it, what is stored, what is async).

Do not ask about rules the docs already settle: stack, DI, SQL style, guard refusal codes, log frontend, goose layout, JSON v2, file size habits.

How to ask:

1. One line: what is unclear and what breaks if the wrong option ships.
2. A numbered list of concrete options (usually 2–4). Each option is a design, not a vibe.
3. Mark exactly one option `recommended` and give one sentence of why (constraint in this repo, blast radius, or fit with an existing package).
4. Always add a final free-input option so the human can name a different path in their own words.
5. Wait. Do not implement, generate, or migrate until they pick a number or write their own.

```text
Unclear: <decision> — wrong pick means <cost>.

1. <option> — <one-line consequence>
2. <option> — <one-line consequence>  [recommended: <one reason>]
3. <option> — <one-line consequence>
4. Other — reply with the design you want
```

If they already chose in the same thread, do not re-ask. If new facts change the choice, confirm the delta only.

## Workflow

- **Cross-check the contract.** Frontend implementation and integration work must be cross-checked against the actual backend state before it is written: the proto specs (`api/connect/*.proto`) name the procedures and wire types, and the serving module (`internal/`, `modules/`) names what really answers — which surface owns which key, and which transport serves it. A plan's or doc's claim is a hypothesis until this check confirms it; a toggle, endpoint, or envelope shape that exists on neither side is not to be invented.
- **Issues.** A finding that outlives the turn — a defect discovered mid-work, a gap a plan's closure exposed, a parked decision with a named trigger — goes to `.llms/issues/issue-YYYYMMDD_hhmm.md` when found, not to chat memory. The file's frontmatter carries the rollup `status` and `captured`/`updated` timestamps; each item inside carries its own `status` line, the plan documents it came from or touches, and the trigger that reopens it. Statuses: `open`, `considering`, `resolved`, `wontfix`. When an item resolves, mark it in the file it lives in — the file is the tracker, not a diary.
- **Docs sync.** A change that lands a behavior must land its documentation in the same turn: contract or invariant → `.llms/rules.md`; reasoning, decision, or library record → `.llms/architecture.md`; a shipped endpoint, job, email, audit event, guard rule, or settings key → the endpoint reference, the Yaak collection, and `docs/` where they live. Decide once per turn, before the commit: if the diff changes what a doc claims, the doc rides the same commit. Outdated prose is a defect of the change, not a follow-up.
- **Validation.** `go test ./...`, `task lint`, and a probe against a freshly built `build/release/saka serve --env-file=.env.local` before claiming a change works — a green unit suite does not exercise the composition root. Load tests run only when the user asks for them; never start one unprompted.
- **Plans.** A plan (`.llms/plans/`) is a contract with a fixed shape and hard rules; it executes only on the owner's explicit instruction. The full spec is in `.llms/rules.md` ("Plan documents").
- **Prose.** Tree documents are English. Technical documentation for agents lives in `.llms/`; product documentation for humans lives in `docs/` — keep `docs/` free of technical references (paths, internals, agent context); its orientation is the human reader. Chat may follow the user's language. Files do not. Use skill `clarity` before writing or rewriting any of them; `handoff` for handovers.
- **Library docs.** Use the MCP Context7 and DeepWiki tools for external libraries; the module cache is the pinned truth — code wins over docs.
- **Secrets.** A secret is listed in `internal/config` `secretKeys` and nowhere else. Never print one; never rewrite a dotenv file without consent.

## Git

- Stage explicit paths. Check `git status`. Never `git add -A` or `git add .`.
- Message: `{feat,fix,docs,refactor,chore}[(scope)]: <concise message>`. Do not push.
- Do not commit unless this turn asked for it. When the work is finished, recommend the one-line message and list the paths.
- Never run `git reset --hard`, `git checkout .`, `git clean -fd`, `git stash`, `git commit --no-verify`, or `git push --force`.

## Related Docs

- `.llms/stylex-authoring.md` — read before writing styles. `.llms/stylex-installation.md` — StyleX setup.
