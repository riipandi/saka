# Agents Instructions

This file holds the workflow rules, always in force; the detail — the repository's shape, stack, per-package contracts, and reasoning — lives in `.llms/`. Read in this order:

1. This file — behavioral rules and workflow.
2. `.llms/index.md` — the documentation map.
3. `.llms/shape.md` — the repository's shape: the layout, the toolchain, the frontend conventions, the porting contract. Read once, return when a path or a command is needed.
4. `.llms/rules.md` — the detailed rules: tasks, per-package contracts, how-to-change recipes, traps. Read the section a change touches before touching it.
5. `.llms/architecture.md` — per-package reasoning and library decision records. Read that package's section before changing it.

## Working rules

- Do not document, test, or treat a stub as working — implemented surfaces are listed in `.llms/index.md` and `.llms/architecture.md`; the rest is a scaffold.
- Do not add, remove, or rename a top-level directory unless asked; extend an existing package.
- Porting a plan into code means implementing it. A doc may describe a larger surface than the code has.
- No self-evident comments: a comment must carry what the code cannot (a why, a contract, a trap), never restate what the next line already says.

## Skills

The agent environment carries a skill library. These are the ones this repository reaches for, by area. A skill advises; the rules in this file and `.llms/` still win.

- **The documentation ledger** — `agent-docs` for every piece of ledger work: a plan document, an issue, a roadmap entry, a handover, an audit, an index update, or a `docs/` change. Its `references/` folder carries the exemplar shapes — read the matching one before writing that kind of document.
- **Frontend UI** — `emil-design-eng` and `apple-design` when composing or implementing UI; `impeccable` for a design review, polish pass, or an interface that needs shaping or critique.
- **Backend services** — the per-stack DI/design skills the architecture document names; hardening skills when writing or reviewing `net/http` handlers and middleware; the profiling skill when a latency or throughput complaint lands — profile first, never tune blind.
- **Databases** — the query/index optimization skill for query work; the code-review skill when reviewing a migration or a query change.
- **E2E** — the browser-driving skill for a running app; the test-generation skill for a new scenario in the E2E package.
- **Security** — `security-review` before an auth, federation, or storage change claims done; the audit records in `.llms/audit/` are the format.
- **Prose** — `clarity` before writing or rewriting any tree document; `handoff` for handovers.

## Ambiguous decisions

Stop and confirm before writing code, migrations, protos/schemas/interfaces, or config when scope, system design, or an architecture choice is unclear. Do not pick a design silently and ship it.

Ask when any of these is true:

- The request fits more than one package, area, table, or transport.
- A new surface, job, queue, config key, grant, or migration is implied but not named.
- `.llms/architecture.md` and this file leave more than one valid shape.
- The change would be hard to undo (schema, public contract, seed, authz rule, secret, storage key).
- You would have to invent a product rule (who can call it, what is stored, what is async).

Do not ask about rules the docs already settle — check `.llms/rules.md`'s contracts first; a settled rule is not a question.

How to ask:

1. One line: what is unclear and what breaks if the wrong option ships.
2. A numbered list of concrete options (usually 2–4). Each option is a design, not a vibe.
3. Mark exactly one option `recommended` and give one sentence of why (a constraint in this repo, blast radius, or fit with an existing package).
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

- **Cross-check the contract.** Integration work is cross-checked against the actual backend state before it is written: the wire contracts (the API specs and proto files) name the procedures and wire types, and the serving module names what really answers — which surface owns which key, and which transport serves it. A plan's or doc's claim is a hypothesis until this check confirms it; a toggle, endpoint, or envelope shape that exists on neither side is not to be invented.
- **Issues.** A finding that outlives the turn — a defect discovered mid-work, a gap a plan's closure exposed, a parked decision with a named trigger — goes to `.llms/issues/issue-YYYYMMDD_hhmm.md` when found, not to chat memory. The file's frontmatter carries the rollup `status` and `captured`/`updated` timestamps; each item carries its own `status` line, the plan documents it came from or touches, and the trigger that reopens it. Statuses: `open`, `considering`, `resolved`, `wontfix`. A file whose every item reaches a terminal status leaves the live directory for `.llms/archive/issue-<stamp>.md` — flat, under its existing name, and the index's recap row drops with it; the live directory lists only open and considering files. The `agent-docs` skill carries the full shape.
- **Docs sync.** A change that lands a behavior must land its documentation in the same turn: contract or invariant → `.llms/rules.md`; reasoning, decision, or library record → `.llms/architecture.md`; a shipped surface (endpoint, job, background delivery, guard rule, settings key) → the endpoint reference, the API client collection the repository keeps, and `docs/` where they live. Decide once per turn, before the commit: if the diff changes what a doc claims, the doc rides the same commit. Outdated prose is a defect of the change, not a follow-up. Every completed task or plan phase runs the closing sweep the `agent-docs` skill carries: the plan's own status, the issue and roadmap statuses it touches, the `docs/` tree it made stale, the index rows, and the handover — each line synced or recorded n/a.
- **Validation.** The full test suite, the lint task, and a probe against a freshly built release binary before claiming a change works — a green unit suite does not exercise the composition root. Load tests run only when the user asks for them; never start one unprompted.
- **Static analysis.** Findings from the static-analysis tooling (`.llms/shape.md` carries the commands) are leads, not deletion orders: no tool sees through the reachability it does not model — a method satisfied through an interface it did not trace, a handler registered by reflection or `init` — so verify each report against the tree before removing anything, and a finding that outlives the turn goes to `.llms/issues/` like any other.
- **Plans.** A plan (`.llms/plans/`) is a contract with a fixed shape and hard rules; it executes only on the owner's explicit instruction. The full spec is in `.llms/rules.md` ("Plan documents") and the `agent-docs` skill.
- **Prose.** Tree documents are English. Technical documentation for agents lives in `.llms/`; product documentation for humans lives in `docs/` — keep `docs/` free of technical references (paths, internals, agent context); its orientation is the human reader. Chat may follow the user's language. Files do not. The `clarity` skill is mandatory before writing or rewriting any of them; `handoff` for handovers.
- **Library docs.** Use the MCP Context7 and DeepWiki tools for external libraries; the module cache is the pinned truth — code wins over docs.
- **Secrets.** A secret is listed once in the configuration system's secret list and nowhere else. Never print one; never rewrite a dotenv file without consent.

## Git

- Stage explicit paths. Check `git status`. Never `git add -A` or `git add .`.
- Message: `{feat,fix,docs,refactor,chore}[(scope)]: <concise message>`. Do not push.
- Do not commit unless this turn asked for it. When the work is finished, recommend the one-line message and list the paths.
- Never run `git reset --hard`, `git checkout .`, `git clean -fd`, `git stash`, `git commit --no-verify`, or `git push --force`.

## Related docs

The full map is `.llms/index.md`; the entries this file names — shape, rules, architecture, issues, plans, roadmap, audits, handovers — live under `.llms/`.
