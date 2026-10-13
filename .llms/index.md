# Agents Documentation Index

Agent-only documentation. Start at the repo root `AGENTS.md` (rules), then
`.llms/architecture.md` (per-package reasoning and traps). Everything else is
reference, history, or contract — navigate from here.

## Core documents

| Path | Read it for |
| --- | --- |
| `/AGENTS.md` | The general instructions: read order, ambiguous-decision protocol, workflow, git. |
| `.llms/rules.md` | The detailed rules behind `AGENTS.md`: tasks, per-package contracts (multifactor, JWKS, notification, authz, guard, audit, datastore, storage, tus, serving), how-to-change recipes, traps, plan-document spec. |
| `.llms/architecture.md` | Per-package reasoning, kernel/transport/registry contracts, library decision records. Read the relevant section before changing a package. |
| `.llms/auth-design.md` | The authentication design the auth surface follows. |
| `.llms/integration-roadmap.md` | The recommended order for wiring the served backend surface into the webapp, as a delivery checklist (`- [ ]`): what is wired, what each wave lands, the reference map (which acknowledged inspiration each flow consults), the recommended route file layout, the E2E happy/unhappy scenario matrix per flow, the supporting-library research, the shared machinery each piece needs. Analysis and recommendation — each wave becomes a plan on the owner's instruction. |
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
without the owner's explicit instruction. All listed plans are `done` or
`shipped`, except `plan-20261008_1739.md` and `plan-20261014_0047.md`
(both `awaiting-approval`).

| File | Subject |
| --- | --- |
| `plan-20260929_1722.md` | Federation, OIDC, SCIM, device-flow remediation |
| `plan-20260929_1930.md` | OIDC industrialization (Tier 1–2, CIBA, inbound SCIM) |
| `plan-20260930_1728.md` | Passkey authentication, backend first |
| `plan-20261001_1551.md` | Auth alignment: auth-design vs shipped surface |
| `plan-20261001_2312.md` | Migration reorder, table renames, residue cleanup |
| `plan-20261002_1812.md` | Password auth via email-code reverification |
| `plan-20261002_1958.md` | Access restrictions: blocklist + block email subaddresses |
| `plan-20261003_0258.md` | OAuth SSO sign-in (Google, GitHub, custom OIDC) |
| `plan-20261003_0700.md` | Hardening: enumeration, lockout, security notifications |
| `plan-20261003_2359.md` | Storage revamp: buckets, `/storage` mount, tus, signed links — the decisions list (esp. 17–21) is the binding record for the storage engine |
| `plan-20261004_1030.md` | OAuth SSO refinement: full attribute mapping, custom attributes, sign-in profile refresh |
| `plan-20261004_1510.md` | OAuth SSO token retrieval: the stored provider tokens, refresh/rotation, the local offboarding job |
| `plan-20261004_1650.md` | OIDC IdP Standard Claims: `picture` and `updated_at` |
| `plan-20261004_1800.md` | Framework extraction: the reusable engines into `framework/`, composable behind typed Options, `database/` into `internal/`, per-package migration sets — the decisions list (D1–D18) is the binding record for the framework boundary |
| `plan-20261005_1845.md` | OIDC certification readiness: Basic/Config/RP-Initiated-Logout/Back-Channel-Logout OP profiles, the local conformance-suite harness, the audit's recommended fixes — the four profiles rehearse green; the hosted submission run is the owner's (runbook in the handover) |
| `plan-20261008_1739.md` | Component library extraction to `packages/uilibs` — done (P1–P3b landed; P4 closed 2026-10-14 with its docs-sync residue riding `plan-20261014_0047.md` D10) |
| `plan-20261009_0025.md` | Frontend foundation: the cookies library, the backend-driven configuration layer, the shared auth-aware fetch, Connect-Query — the decisions list (D1–D10) is the binding record for the webapp's data layer |
| `plan-20261009_0142.md` | OAuth SSO: one store for every provider, the enabled-provider listing, the SPA flow — the decisions list (D1–D9) is the binding record for the sign-in-with-a-provider surface |
| `plan-20261009_1826.md` | The connect-go v1 → v2 migration — the mechanical import swap, the runtime seams, the follow-ups parked in `issue-20261009_1915.md` |
| `plan-20261010_0054.md` | Wave 1: the account works on real data — profile, picture, danger zone, the session center, email verification & change, the own audit trail, the pagination/error/public-settings machinery — shipped |
| `plan-20261010_0610.md` | Wave 1.5: the authz foundation — the claims snapshot in the store, the `can()` engine (the `framework/authz` matcher ported), `usePermissions()`/`<Can>`, the refusal mapping (D1–D8) |
| `plan-20261010_0925.md` | Wave 1.6: frontend observability — the browser tracer, seam/navigation/worker spans, redaction, the public collector field (D1–D8) — shipped |
| `plan-20261010_1830.md` | Wave 2: sign-in completeness — the MFA challenge & enrollment fork, passkey sign-in/management, password recovery, one-time access, the shared step-up machinery (D1–D8; the one-time access delta is recorded in the phase-6 note) — shipped |
| `plan-20261011_1545.md` | Wave 3: the admin console core — shell & guard, impersonation banner, user center, groups/blocklist/signup tokens, the public signup page, the audit console, the authorization screens (D1–D9) — shipped |
| `plan-20261011_1805.md` | Wave 4: federation administration — OIDC clients (back-channel fields, grant-type editor, the show-once pattern), custom claims & SCIM, the OAuth SSO admin CRUD and the account-side connected-accounts page, the consent ledgers — complete |
| `plan-20261011_2025.md` | Wave 5: platform administration — the settings CRUD screens + TestEmail probe, notifications with the `WatchNotifications` streaming consumer, webhooks, API keys — complete |
| `plan-20261012_0145.md` | The webapp navigation/IA overhaul — sidebar regrouping, the topbar user menu and notification flyout — complete |
| `plan-20261012_0155.md` | Wave 6 (partial): device login approval (`/device`), the ops console (queue + scheduler), the timezone picker; storage intentionally excluded pending the reference inventory (`issue-20261009_2356.md`, now resolved) — done |
| `plan-20261012_1935.md` | The OIDC consent interaction page in the SPA: the completion dialect (header-keyed redirect capture), the consent page, the device grant's verification, the E2E workflow spec (D1–D8) — closes `issue-20261012_0445.md` — done |
| `plan-20261012_2331.md` | The webapp UI revamp: the skeleton loading layer, the long forms riding the side sheet, the structural polish (empty CTAs, list errors with retry, page headers) — done |
| `plan-20261013_0048.md` | The last administrator's deletion guard (backend refusal + the roles-on-the-wire frontend gate) and the singleton builtin provider picker — done |
| `plan-20261014_0047.md` | The open-issues close-out: the dead auth worker's bounded calls, the serve startup check on the administrator grant, AssetsURL's public scope, the registry-derived seed-test totals, the debug E2E compiler boot, the Dockerfile parity guard, the backend auth audit, the handover archive — awaiting-approval |

## Handovers (`.llms/handover/`)

Session compactions, named `handover-<yyyymmdd>_<hhmm>.md`. Only the latest
few matter — each summarizes its session and what stayed open; anything still
true is folded into the core documents above. Newest:
`handover-20261013_0023.md` (the webapp UI revamp; 68 files total; pruning
awaits the owner's nod — `issue-20261006_0050.md` ISS-009).

## Audits (`.llms/audit/`)

Point-in-time reviews against the tree; conclusions age, the tree wins.

- `audit-20260928_0239.md` — post-incident re-audit (deliberate designs vs defects).
- `audit-20260929_1613.md` — federation, device flow, SCIM.
- `audit-20261005_0940.md` — the framework extraction: boundary, concurrency, the tus lock leak fix, the accepted otel/log vulnerability.
- `audit-20261005_1105.md` — the k6 load test: smoke/load/stress over the crucial endpoints, the ceiling not yet reached at ~450 req/s.
- `audit-20261005_1825.md` — the OIDC provider deep pass: metadata-contract fixes (dead `jwks_uri`, closed registration mount), the CAS-loser 500 → `invalid_grant`, the revocation-delete indexes, the live E2E and race harnesses, the specification inventory (implemented vs not, per spec), certification readiness.

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
lands there the moment it is found — not in chat memory. Newest:
`issue-20261012_1605.md`.

Recap (status rollup per file, cross-checked against the tree and the
plans on 2026-10-14 — the files are the tracker, this is the index; the
previous cross-check ran 2026-10-12):

| File | Rollup | Subject |
| --- | --- | --- |
| `issue-20261006_0050.md` | open (12) | The post-certification residues: the owner's hosted cert run (open), the MCP-facing OAuth work + DCR + Form Post + RP-side items (considering ×6), the Yaak prefix split — 84 `[Pocket ID]` vs 138 `[Saka]`, closed wontfix 2026-10-14 (owner keeps the split), the handover archive — 67 files, remedy decided (archive to `.llms/archived`, keep the five newest; plan-20261014_0047), the compose comments (considering); ISS-010 (unused webapp deps) and ISS-012 (the SPA `TODO(frontend)` markers) resolved 2026-10-12 |
| `issue-20261008_1705.md` | open | Auth-wiring accepted risks: the cookie custody model and the tampered profile cookie are `wontfix`, the dual transport is `considering`; the open item is the backend auth audit — accepted into plan-20261014_0047 as its own phase |
| `issue-20261009_0127.md` | considering | The login-screen toggles (`oidc.enabled`, the two one-time-access switches) live only in the deployment document, not the settings catalog — owner decision 2026-10-09 parked the backend move; the REST read stands (re-verified 2026-10-14) |
| `issue-20261009_0345.md` | resolved | A dead auth worker hung every in-flight Comlink call (blank first paint, no console error) — resolved 2026-10-14 (plan-20261014_0047 phase 1): current-engine cell with bounded calls, the error listener revives a wired main-thread engine and interrupted calls re-run against it |
| `issue-20261009_1915.md` | open | Connect-go v2 follow-ups: the v1 module still in `go.mod` as buf's indirect (re-verified 2026-10-14), validate/otelconnect have no `/v2` yet, and the v2 handler-error masking convention (rules.md record rides the plan's closing phase). The two-suite E2E build split resolved 2026-10-14 (plan-20261014_0047 phase 5: the ladder boots its own compiler, the suite's server lives on :4473, no reuse) |
| `issue-20261009_2356.md` | resolved | The endpoint reference's missing Storage section — landed 2026-10-12 (five bucket procedures, five tus routes, the `/storage` mount); the Wave 6 storage item is unblocked |
| `issue-20261010_0017.md` | resolved | The Yaak reconciliation: three items resolved (the `ConfirmEmailChange` row, the stale password-recovery rows, the `GetLinkedAccountTokens` request), `AssetsURL`'s public-scope claim — resolved 2026-10-14 (plan-20261014_0047 phase 3): the field joins the public body, the publisher test pins it |
| `issue-20261011_2205.md` | resolved | Catalog drift leaves existing deployments with stale administrator grants — resolved 2026-10-14 (plan-20261014_0047 phase 2): the serve startup check logs the lagging slugs and the repair command, never writes |
| `issue-20261012_0445.md` | resolved | The OIDC consent interaction page shipped (plan-20261012_1935: the SPA interaction area — consent page, device verification, the completion dialect) |
| `issue-20261012_1538.md` | resolved | Hardcoded seeder counts in `cmd/migrate_seed_test.go` drifted on every new seed item — resolved 2026-10-14 (plan-20261014_0047 phase 4): the tests read the run's own status line and assert the registry's invariants, no counter to maintain |
| `issue-20261012_1605.md` | open | `vp run` crashes children with a native stack overflow in Linux containers — the Dockerfile workaround (direct `tsc` chain, re-verified 2026-10-14) stands until a fixed vp ships; report upstream |

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
