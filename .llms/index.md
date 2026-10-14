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
without the owner's explicit instruction. The directory keeps only the
plans that still hold open work — a completed plan archives flat to
`.llms/archive/plan-<stamp>.md` under its existing name (2026-10-14: the
seven completed plans, `plan-20261008_1739.md` through
`plan-20261014_0915.md`, moved; `plan-20261014_0930.md` alone remains).
The archive carries the plans' own subject lines and decision records;
the older history (35 files) lives there too.

| File | Subject |
| --- | --- |
| `plan-20261014_0930.md` | The login-toggles and auth-wiring decisions: the deployment document keeps `oidc.enabled` and the two one-time-access switches (owner pick 2026-10-14, re-affirming D7 of plan-20261009_0025), and the two Connect transports are recorded as the worker boundary's shape (D3) — the ownership boundaries recorded in rules/architecture/docs, both issues closed `wontfix` and archived — awaiting approval, the phases wait for the owner's word |
| `plan-20261014_0955.md` | `key:rotate`: one command re-seals the six sealed surfaces under a new `APP_SECRET_KEY` (D1 scope, D2 hybrid key supply with `--dry-run`, D3 queue payloads re-seal in the pass, D4 all-or-nothing), the new key written to the env file `key:generate`-style — takes issue-20261014_0949's open residue — awaiting approval |

## Handovers (`.llms/handover/`)

Session compactions, named `handover-<yyyymmdd>_<hhmm>.md`. Every handover —
live and archived alike — carries the frontmatter convention
(`status` / `captured` / `plan` / `wave`; `captured` derives mechanically from
the filename stamp, the other keys from the body, and a key the body cannot
name is omitted rather than guessed). The directory keeps the five newest; the
older history (67 files) lives flat in
`.llms/archive/handover-<stamp>.md`, moved under their existing names —
each summarizes its session and what stayed open; anything still true is
folded into the core documents above. Newest:
`handover-20261014_0945.md` (the issues ledger reshuffle; the live items
re-homed, the five rollups archived, plan-20261014_0930's phase 2
slimmed to handover + index sync).

## Audits (`.llms/audit/`)

Point-in-time reviews against the tree; conclusions age, the tree wins.
Every audit carries the frontmatter convention (`status` / `updated` /
`reference`): `status` is `resolved` when every finding the audit demanded
is closed in the tree (an owner-accepted risk recorded in the decision
records does not hold the audit open) or `open` while a finding waits on
its issue or plan; `reference` names the plan and issue that carry the
outcome, omitted when direct commits closed the findings.

- `audit-20260928_0239.md` — post-incident re-audit (deliberate designs vs defects). resolved.
- `audit-20260929_1613.md` — federation, device flow, SCIM. resolved (plan-20260929_1722).
- `audit-20261005_0940.md` — the framework extraction: boundary, concurrency, the tus lock leak fix, the accepted otel/log vulnerability. resolved.
- `audit-20261005_1105.md` — the k6 load test: smoke/load/stress over the crucial endpoints, the ceiling not yet reached at ~450 req/s. resolved.
- `audit-20261005_1825.md` — the OIDC provider deep pass: metadata-contract fixes (dead `jwks_uri`, closed registration mount), the CAS-loser 500 → `invalid_grant`, the revocation-delete indexes, the live E2E and race harnesses, the specification inventory (implemented vs not, per spec), certification readiness. resolved (plan-20261005_1845; the hosted submission run stays open in issue-20261006_0050).
- `audit-20261014_0329.md` — the backend auth audit (sessions, refresh rotation, revocation): the inactivity gate's rolled-back revocation fixed, the gate's mis-aimed test re-pointed, the EXPLAIN probes at 200k rows, the replay pattern judged sound, the missing session sweep opened as `issue-20261014_0330.md`. resolved — the sweep closed with `plan-20261014_0504.md`.

## Roadmap (`.llms/roadmap/`)

Parked future improvements and the delivery analyses — not plans, not
issues: an entry here carries its own `status` / `updated` frontmatter
(line), the unlock that makes it worth planning, and the source issue it
came from; when an unlock fires the entry re-enters `.llms/issues/` or
goes straight to a plan. The directory's documents are `living` (updated
in place, stamped) rather than point-in-time records.

- `future-improvements.md` — the parked decisions the issue ledger
  stopped carrying (2026-10-14): the six OIDC/federation entries
  (RFC 8707 resource indicators, internal MCP servers, DCR, Form Post
  OP, the RP conformance rehearsal, RP-side logout), the two CLI/tooling
  entries (`storage:migrate`, the deferred `rs/cors` replacement), and
  the three upstream-gated dependency/toolchain entries (connect v1 out
  of `go.mod`, validate/otelconnect `/v2`, the Dockerfile's `vp run`
  restoration). Sources: issue-20261014_0935, issue-20261014_0949, and
  the archived 0936/0937.
- `frontend-integration.md` — the recommended order for wiring the
  served backend surface into the webapp, as a delivery checklist
  (`- [ ]`): what is wired, what each wave lands, the reference map
  (which acknowledged inspiration each flow consults), the recommended
  route file layout, the E2E happy/unhappy scenario matrix per flow, the
  supporting-library research, the shared machinery each piece needs.
  Analysis and recommendation — each wave becomes a plan on the owner's
  instruction.

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
name — the live directory lists only what is open or considering. A
rollup that still carries live items may also archive, its open and
considering items re-homed first into a fresh issue file that names the
source (2026-10-14: the five 2026-10-06 → 2026-10-12 rollups archived,
their live items carved into the files below; later the same day the
aspirational items moved on to the roadmap — 0936 and 0937 archived
whole, 0935 and 0949 shrunk — so a parked improvement lives in
`.llms/roadmap/`, not as a floating issue).
Newest: `issue-20261014_0949.md`.

Recap (status rollup per file, cross-checked against the tree and the
plans on 2026-10-14 — the files are the tracker, this is the index; the
previous cross-check ran 2026-10-12):

| File | Rollup | Subject |
| --- | --- | --- |
| `issue-20261014_0935.md` | open (1) + considering (1) | The live residues of the 2026-10-06 certification rollup after two carves: the owner's hosted certification run (open, owner's action) and the commented compose blocks (owner's call). The six parked protocol/direction decisions (ISS-002–007) moved to the roadmap 2026-10-14 as RI-1–RI-6; the terminal items stay with the archive |
| `issue-20261014_0949.md` | open (1) + one resolved | The reconciliation of the fifteen `status: partial` archived handovers + plan-20261012_0155's partially-done phase (2026-10-14): nearly everything landed — the open residue is the `key:rotate` stub (`cmd/appkey.go:177`, plan-20261014_0955 takes it); the `release_after` invariant resolved on re-verification (enforced at the queue client's `Register` seam, the config layer cannot see task timeouts); the two parked decisions moved to the roadmap as RI-7/RI-8 |

## Finding history fast

- **A design decision** → the plan's Decisions section (dated, with rejected alternatives); the storage model's decisions live in `plan-20261003_2359.md`.
- **An open item** → `.llms/issues/` (above).
- **What changed when** → `git log --oneline`; commit subjects follow `{feat,fix,docs,refactor,chore}[(scope)]:`.
- **What a past session did** → the handover of that date; memory recall supplements it.
