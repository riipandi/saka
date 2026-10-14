---
name: agent-docs
description: >
    The Saka documentation-ledger workflow: drafting plan documents
    (.llms/plans), filing and reconciling issues (.llms/issues), curating
    the roadmap (.llms/roadmap), writing handovers and audits, keeping
    .llms/index.md and the .llms core documents in sync, and maintaining
    the human-facing docs/ tree. Use when the user asks for a plan, an
    issue, a roadmap entry, a handover, an index update, or any docs/
    change — and when the user runs /agent-docs.
metadata:
    short-description: "The .llms ledger and docs/ workflow"
---

# Agent docs — the Saka ledger workflow

The shapes in force are the exemplars in `reference/` beside this file.
Read the matching one before writing its kind; this file carries the
rules, the reference carries the shape. `AGENTS.md` and `.llms/rules.md`
("Plan documents") win over anything here.

## The two audiences

- **`.llms/` is for agents** — English, technical, file:line evidence,
  no prose padding. `index.md` is the map; `rules.md` holds contracts,
  `architecture.md` holds reasoning and decision records.
- **`docs/` is for humans** — no internals, no paths, no agent context;
  orientation on the reader. Run the `clarity` skill before writing or
  rewriting any tree document.

## Plans (`.llms/plans/plan-YYYYMMDD_HHMM.md`)

A plan is a contract, written only on the owner's request. Shape:
frontmatter (`status: awaiting-approval` → in progress → `done`,
`updated`, `phases:` with `id`/`title`/`status`/`note`), then Objective,
Current state (verified file:line facts — every claim checked in the
tree first), Decisions (dated, each with its rejected alternatives),
a shape/enforcement table, Execution rules, Phases (each with its own
validation line and suggested commit message), Test matrix, Explicitly
out of scope.

Execution rules (the owner's standing template — mirror it in every
plan): execution waits for the owner's explicit "Lanjut fase N"; atomic
commit per phase with the plan-named message; self-serve commits allowed
during the plan's working session; after the last phase, stop and
recommend the closing commit message — never commit it unasked. The
final-validation phase and the test matrix are mandatory; E2E is written
only when relevant and recorded n/a otherwise. Early stage: no backward
compatibility, no legacy code; backend: no `ALTER` — edit migrations in
place, dev-DB resets allowed when the plan says so.

Ledger hygiene: a completed plan archives flat to
`.llms/archive/plan-<stamp>.md` under its existing name; the live
directory keeps only plans with open work. The index's plans preamble
and rows move with it.

## Issues (`.llms/issues/issue-YYYYMMDD_HHMM.md`)

A finding that outlives the turn lands here the moment it is found, not
in chat memory. Frontmatter: `status` (the rollup), `captured`,
`updated`, `summary`, `count`. One file per capture; each item carries
its own status line — `open`, `considering` (a decision parked with a
named trigger), `resolved`, `wontfix` — plus `area`, `plan` references
(source or record-to-honor), `summary`, `trigger`.

Verify before writing: every file:line claim is checked against the tree
in the same turn, and re-checked when the item is touched — a stale
finding is worse than a missing one (the `release_after` lesson:
re-verification flipped an open item to resolved).

Hygiene: a file whose every item is terminal archives flat under its
existing name; a rollup with live items may also archive after its open
items are re-homed into a fresh issue file that names the source — or
moved to the roadmap when they are parked improvements, not work items
(`.llms/roadmap/`). Annotate the archived copy with where its live
content went. After any move, grep the live tree for dangling references
to the old path.

## Roadmap (`.llms/roadmap/`)

Parked future improvements and delivery analyses — not plans, not
issues. Frontmatter per document: `status: living`, `updated`,
`references` (source issues/plans). Each entry carries its own
`- status: parked (unlock: …) · updated <date>` line, the verified
evidence (file:line), the unlock that fires it, and a first move. When
an unlock fires, the entry re-enters `.llms/issues/` or goes straight to
a plan — the roadmap is not a graveyard, it is a staging area.

## Handovers (`.llms/handover/handover-YYYYMMDD_HHMM.md`) and audits

Handovers are session compactions: frontmatter `status` / `captured` /
`plan` / `wave` — a key the body cannot name is omitted, not guessed.
The directory keeps the five newest; older history archives flat. Audits
(`.llms/audit/`) are point-in-time reviews with the same frontmatter
convention; conclusions age, the tree wins.

## The docs-sync rule

A change that lands a behavior lands its documentation in the same turn
and the same commit. Dispatch: a contract or invariant → `.llms/rules.md`;
a reasoning/decision/library record → `.llms/architecture.md`; a shipped
endpoint, job, email, audit event, guard rule, or settings key → the
endpoint reference, the Yaak collection, and `docs/`. The index's rows
move in the same commit — plans gained or archived, issues opened or
closed, pointers (`Newest:`) updated. Outdated prose is a defect of the
change, not a follow-up.

## Mechanical traps

- Never batch multiple `edit_file` calls to the same file in one
  parallel block — the writes race and one edit is silently lost. Same
  file: sequential, one `old_string` at a time, verified after each.
- Close every ledger batch with a count check (`grep -c` on the status
  lines) and a dangling-reference grep after every move.
- Stage explicit paths; never `git add -A`.

## Reference exemplars

| File | The shape it shows |
| --- | --- |
| `reference/plan-example.md` | A full plan: frontmatter phases, verified current state, decisions with rejected alternatives, the execution-rules block, per-phase commits, matrix, out-of-scope |
| `reference/issue-example.md` | An issue rollup: frontmatter rollup, per-item statuses with triggers, the settled-records section |
| `reference/roadmap-example.md` | A roadmap document: frontmatter, per-entry `parked (unlock)` lines, evidence, first moves, source stamps |
| `reference/handover-example.md` | A handover: frontmatter convention, what the session did, the commits, what stayed open |
