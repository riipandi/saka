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
`shipped`.

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
| `plan-20261004_1800.md` | Framework extraction: the reusable engines into `framework/`, composable behind typed Options, `database/` into `internal/`, per-package migration sets — the decisions list (D1–D18) is the binding record for the framework boundary |
| `plan-20261005_1845.md` | OIDC certification readiness: Basic/Config/RP-Initiated-Logout/Back-Channel-Logout OP profiles, the local conformance-suite harness, the audit's recommended fixes — the four profiles rehearse green; the hosted submission run is the owner's (runbook in the handover) |
| `plan-20261009_0025.md` | Frontend foundation: the cookies library, the backend-driven configuration layer, the shared auth-aware fetch, Connect-Query — the decisions list (D1–D10) is the binding record for the webapp's data layer |
| `plan-20261009_0142.md` | OAuth SSO: one store for every provider, the enabled-provider listing, the SPA flow — the decisions list (D1–D9) is the binding record for the sign-in-with-a-provider surface |
| `plan-20261010_0054.md` | Wave 1: the account works on real data — profile, picture, danger zone, the session center, email verification & change, the own audit trail, the pagination/error/public-settings machinery — **awaiting approval** |

## Handovers (`.llms/handover/`)

Session compactions, named `handover-<yyyymmdd>_<hhmm>.md`. Only the latest
few matter — each summarizes its session and what stayed open; anything still
true is folded into the core documents above. Newest:
`handover-20261006_0138.md`.

## Audits (`.llms/audit/`)

Point-in-time reviews against the tree; conclusions age, the tree wins.

- `audit-20260928_0239.md` — post-incident re-audit (deliberate designs vs defects).
- `audit-20260929_1613.md` — federation, device flow, SCIM.
- `audit-20261005_0940.md` — the framework extraction: boundary, concurrency, the tus lock leak fix, the accepted otel/log vulnerability.
- `audit-20261005_1105.md` — the k6 load test: smoke/load/stress over the crucial endpoints, the ceiling not yet reached at ~450 req/s.
- `audit-20261005_1825.md` — the OIDC provider deep pass: metadata-contract fixes (dead `jwks_uri`, closed registration mount), the CAS-loser 500 → `invalid_grant`, the revocation-delete indexes, the live E2E and race harnesses, the specification inventory (implemented vs not, per spec), certification readiness.

## Product docs (`docs/`)

For humans, not agents: `api-endpoint.md`, `api-response.md`,
`product-guide.md`, `oauth-sso.md`, `oidc.md`, `oidc-profiles.md`,
`deployment.md`. The rule `AGENTS.md`
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
`issue-20261010_0017.md`.

## Finding history fast

- **A design decision** → the plan's Decisions section (dated, with rejected alternatives); the storage model's decisions live in `plan-20261003_2359.md`.
- **An open item** → `.llms/issues/` (above).
- **What changed when** → `git log --oneline`; commit subjects follow `{feat,fix,docs,refactor,chore}[(scope)]:`.
- **What a past session did** → the handover of that date; memory recall supplements it.
