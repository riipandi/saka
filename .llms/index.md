# Agents Documentation Index

Agent-only documentation. Start at the repo root `AGENTS.md` (rules), then
`.llms/architecture.md` (per-package reasoning and traps). Everything else is
reference, history, or contract — navigate from here.

## Core documents

| Path | Read it for |
| --- | --- |
| `/AGENTS.md` | The general instructions: read order, ambiguous-decision protocol, the per-area skill map, workflow, git. |
| `.llms/rules.md` | The detailed rules behind `AGENTS.md`: tasks, per-package contracts (multifactor, JWKS, notification, authz, guard, audit, datastore, storage, tus, serving), how-to-change recipes, traps, plan-document spec. |
| `.llms/architecture.md` | Per-package reasoning, kernel/transport/registry contracts, library decision records. Read the relevant section before changing a package. |
| `.llms/auth-design.md` | The authentication design the auth surface follows. |
| `.llms/roadmap/frontend-integration.md` | The recommended order for wiring the served backend surface into the webapp, as a delivery checklist (`- [ ]`): what is wired, what each wave lands, the reference map (which acknowledged inspiration each flow consults), the recommended route file layout, the E2E happy/unhappy scenario matrix per flow, the supporting-library research, the shared machinery each piece needs. Analysis and recommendation — each wave becomes a plan on the owner's instruction. |
| `.llms/upstream-deviations.md` | Every intentional departure from Pocket ID upstream, with reasons. |
| `.llms/endpoint-reference.md` | Shipped endpoints vs the ported upstream reference, at a glance. |
| `.llms/references/pocket-id-api-reference.md` | The upstream API surface the port maps onto. |
| `.llms/references/supabase-storage-schema.sql`, `database-reference.sql` | Storage-model source material and the database's table inventory. |

## Docs sync

`AGENTS.md` carries the rule: a behavior change and its documentation land in
the same turn and the same commit. This file is the map the rule dispatches
on — when a diff touches what a document claims, update that document here
and nowhere else. A plan's closing phase owns the full sweep (see
`.llms/rules.md`, "Plan documents"); ordinary turns only touch the documents
the diff actually made stale.

## Plans (`.llms/plans/`)

A plan is a contract: frontmatter `status`, decisions with rejected
alternatives, phases with commits. Never start, resume, or continue one
without the owner's explicit instruction. The directory keeps the five
newest plans; the older history (27 files) lives flat in
`.llms/archive/plan-<stamp>.md`, moved under their existing names — the
archive files carry their own subject lines and decision records. All
retained plans are `done`, except `plan-20261014_0047.md`
(`awaiting-approval`: phases 1–8 landed, the closing phase is next).

| File | Subject |
| --- | --- |
| `plan-20261012_0155.md` | Wave 6 (partial): device login approval (`/device`), the ops console (queue + scheduler), the timezone picker; storage intentionally excluded pending the reference inventory (`issue-20261009_2356.md`, now resolved) — done |
| `plan-20261012_1935.md` | The OIDC consent interaction page in the SPA: the completion dialect (header-keyed redirect capture), the consent page, the device grant's verification, the E2E workflow spec (D1–D8) — closes `issue-20261012_0445.md` — done |
| `plan-20261012_2331.md` | The webapp UI revamp: the skeleton loading layer, the long forms riding the side sheet, the structural polish (empty CTAs, list errors with retry, page headers) — done |
| `plan-20261013_0048.md` | The last administrator's deletion guard (backend refusal + the roles-on-the-wire frontend gate) and the singleton builtin provider picker — done |
| `plan-20261014_0047.md` | The open-issues close-out: the dead auth worker's bounded calls, the serve startup check on the administrator grant, AssetsURL's public scope, the registry-derived seed-test totals, the debug E2E compiler boot, the Dockerfile parity guard, the backend auth audit, the handover archive — done (phases 1–8 committed per phase; the owner's closing commit `a70880e1` completes phase 9) |

## Handovers (`.llms/handover/`)

Session compactions, named `handover-<yyyymmdd>_<hhmm>.md`. Every handover —
live and archived alike — carries the frontmatter convention
(`status` / `captured` / `plan` / `wave`; `captured` derives mechanically from
the filename stamp, the other keys from the body, and a key the body cannot
name is omitted rather than guessed). The directory keeps the five newest; the
older history (62 files) lives flat in
`.llms/archive/handover-<stamp>.md`, moved under their existing names —
each summarizes its session and what stayed open; anything still true is
folded into the core documents above. Newest:
`handover-20261014_0325.md` (the open-issues close-out).

## Audits (`.llms/audit/`)

Point-in-time reviews against the tree; conclusions age, the tree wins.

- `audit-20260928_0239.md` — post-incident re-audit (deliberate designs vs defects).
- `audit-20260929_1613.md` — federation, device flow, SCIM.
- `audit-20261005_0940.md` — the framework extraction: boundary, concurrency, the tus lock leak fix, the accepted otel/log vulnerability.
- `audit-20261005_1105.md` — the k6 load test: smoke/load/stress over the crucial endpoints, the ceiling not yet reached at ~450 req/s.
- `audit-20261005_1825.md` — the OIDC provider deep pass: metadata-contract fixes (dead `jwks_uri`, closed registration mount), the CAS-loser 500 → `invalid_grant`, the revocation-delete indexes, the live E2E and race harnesses, the specification inventory (implemented vs not, per spec), certification readiness.
- `audit-20261014_0329.md` — the backend auth audit (sessions, refresh rotation, revocation): the inactivity gate's rolled-back revocation fixed, the gate's mis-aimed test re-pointed, the EXPLAIN probes at 200k rows, the replay pattern judged sound, the missing session sweep opened as `issue-20261014_0330.md`.

## Product docs (`docs/`)

For humans, not agents: `api-endpoint.md`, `api-response.md`,
`product-guide.md`, `users.md`, `auth.md`, `oauth-sso.md`, `oidc.md`,
`oidc-profiles.md`, `deployment.md`. The rule `AGENTS.md`
carries: no technical references here — internals, engine paths, and agent
context belong in `.llms/`; keep the orientation on the human reader. Keep
aligned when a shipped surface changes.

## Issues (`.llms/issues/`)

The open and considering items, newest capture first:
`issue-YYYYMMDD_hhmm.md` with a frontmatter rollup (`status`, `captured`,
`updated`) and per-item status lines carrying their plan references and
reopen triggers. The
rule lives in `AGENTS.md` ("Issues"): a finding that outlives the turn
lands there the moment it is found — not in chat memory. An issue file
whose every item reaches a terminal status (`resolved`) leaves the
directory for `.llms/archive/issue-<stamp>.md`, flat, under its existing
name — the live directory lists only what is open or considering.
Newest: `issue-20261014_0330.md`.

Recap (status rollup per file, cross-checked against the tree and the
plans on 2026-10-14 — the files are the tracker, this is the index; the
previous cross-check ran 2026-10-12):

| File | Rollup | Subject |
| --- | --- | --- |
| `issue-20261006_0050.md` | open (12) | The post-certification residues: the owner's hosted cert run (open), the MCP-facing OAuth work + DCR + Form Post + RP-side items (considering ×6), the Yaak prefix split — 84 `[Pocket ID]` vs 138 `[Saka]`, closed wontfix 2026-10-14 (owner keeps the split), the handover archive — resolved 2026-10-14 (plan-20261014_0047 phase 8: 62 handovers + 27 plans moved flat to `.llms/archive/`, the five newest of each retained), the compose comments (considering); ISS-010 (unused webapp deps) and ISS-012 (the SPA `TODO(frontend)` markers) resolved 2026-10-12 |
| `issue-20261008_1705.md` | considering | Auth-wiring accepted risks: the cookie custody model and the tampered profile cookie are `wontfix`, the dual transport stays `considering`; the backend auth audit resolved 2026-10-14 (audit-20261014_0329, plan-20261014_0047 phase 7 — the gate's rolled-back revocation fixed, indexes proven, the sweep gap opened as issue-20261014_0330) — no open item remains |
| `issue-20261009_0127.md` | considering | The login-screen toggles (`oidc.enabled`, the two one-time-access switches) live only in the deployment document, not the settings catalog — owner decision 2026-10-09 parked the backend move; the REST read stands (re-verified 2026-10-14) |
| `issue-20261014_0330.md` | open | Session rows are never swept — the cleanup job covers queue, MFA, webauthn, signup, and device-login rows but not `public.sessions`; a retention sweep (ended past N days) wants a retention key and a named surface. Found by the backend auth audit (audit-20261014_0329) |
| `issue-20261009_1915.md` | open | Connect-go v2 follow-ups: the v1 module still in `go.mod` as buf's indirect (re-verified 2026-10-14) and validate/otelconnect have no `/v2` yet — both upstream-gated. The E2E build split resolved 2026-10-14 (phase 5: the ladder boots its own compiler, :4473, no reuse); the handler-error masking convention resolved 2026-10-14 (the rules.md record landed with the closing commit) |
| `issue-20261012_1605.md` | open | `vp run` crashes children with a native stack overflow in Linux containers — the Dockerfile workaround (direct `tsc` chain, re-verified 2026-10-14) stands until a fixed vp ships; the parity guard (`scripts/check-typecheck-parity.sh`, run by the pre-commit hook, landed 2026-10-14 via plan-20261014_0047 phase 6) fails the commit that drifts either side; report upstream |

## Finding history fast

- **A design decision** → the plan's Decisions section (dated, with rejected alternatives); the storage model's decisions live in `plan-20261003_2359.md`.
- **An open item** → `.llms/issues/` (above).
- **What changed when** → `git log --oneline`; commit subjects follow `{feat,fix,docs,refactor,chore}[(scope)]:`.
- **What a past session did** → the handover of that date; memory recall supplements it.
m** → `.llms/issues/` (above).
- **What changed when** → `git log --oneline`; commit subjects follow `{feat,fix,docs,refactor,chore}[(scope)]:`.
- **What a past session did** → the handover of that date; memory recall supplements it.
neline`; commit subjects follow `{feat,fix,docs,refactor,chore}[(scope)]:`.
- **What a past session did** → the handover of that date; memory recall supplements it.
of that date; memory recall supplements it.
