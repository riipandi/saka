---
name: agent-docs
description: >
    The documentation-ledger workflow: drafting plan documents
    (.llms/plans), filing and reconciling issues (.llms/issues), curating
    the roadmap (.llms/roadmap), writing handovers and audits, keeping
    .llms/index.md and the .llms core documents in sync, and maintaining
    the human-facing docs/ tree. Use when the user asks for a plan, an
    issue, a roadmap entry, a handover, an index update, or any docs/
    change — and when the user runs /agent-docs.
metadata:
    short-description: "The .llms ledger and docs/ workflow"
---

# Agent docs — the documentation ledger workflow

The shapes in force are the exemplars in `references/` beside this file.
Read the matching one before writing its kind; this file carries the
rules, the reference carries the shape. The repository's own instruction
files (AGENTS.md and the core documents it names) win over anything
here. The exemplars are snapshots of one repository's ledger — their
shape is the lesson; their content is not prescriptive.

## The two audiences

- **`.llms/` is for agents** — English, technical, file:line evidence,
  no prose padding. `index.md` is the map; `rules.md` holds contracts,
  `architecture.md` holds reasoning and decision records.
- **`docs/` is for humans** — no internals, no paths, no agent context;
  orientation on the reader. The `clarity` skill is **mandatory** before
  writing or rewriting any document in `.llms/` or `docs/` — a rewrite
  without it is a defect of the change, not a style preference. `docs/`
  prose is concise GFM, one idea per paragraph, emoji only where an
  admonition earns it.

## First run — scaffolding the ledger

When the ledger does not exist yet, build it before using it — and
build it on facts, not invention:

1. **Verify before creating.** Read the actual tree first: the top-level
   layout, where the entry points and packages live, the build/test/lint
   commands the task runner or package scripts actually define, the test
   framework in use, any existing `docs/` or README content worth
   keeping. Every claim a scaffold makes about the codebase must have
   been seen in a file — a command not verified in a script, a package
   not verified on disk, is not written down as fact.
2. **Scaffold the minimum that the workflow needs**, no more:

   ```text
   .llms/
     index.md          — the map: what each document is, the recap table (empty to start)
     rules.md          — Contracts / How to change it / Traps (only verified contracts; name the gaps)
     architecture.md   — per-area reasoning and decision records (one stub entry per area, unverified areas marked so)
     plans/  issues/  roadmap/  handover/  audit/  archive/   — empty directories
   docs/               — an index page naming the human-facing surface (create only if the project has a human-facing surface)
   ```

3. **Stubs stay honest.** A scaffold document that has not earned its
   content yet says so in one line at the top ("scaffold — no verified
   content yet"); it never invents contracts, commands, or decisions to
   look complete. The working rule — do not document a stub as working —
   applies to the ledger itself.
4. **Fill from evidence.** After scaffolding, the first real pass is
   reading the codebase and replacing the stubs' disclaimers with
   verified facts (file:line for contracts, commit or plan references
   for decisions). A scaffold that never graduated past its disclaimer
   is a finding for `.llms/issues/`.
5. The owner's instruction files (AGENTS.md and its kin) are read first
   in this flow too: if they exist, the scaffold must not contradict
   them, and their read-order references must resolve once the
   scaffolding lands.

## Status vocabulary and required metadata

The ledger has four document kinds; each carries its frontmatter keys and
its own status options — never a status from another kind's vocabulary.

| Document | Frontmatter keys (required) | Document `status` options | Item/phase `status` options |
| --- | --- | --- | --- |
| Plan — `.llms/plans/plan-<stamp>.md` | `status`, `updated`, `phases:` (each: `id`, `title`, `status`, `note`) | `awaiting-approval` (drafted, unstarted) · `in-progress` (phases running) · `done` (every phase landed, matrix green) · `cancelled` (owner closed it; the note names why) | `pending` (not started) · `in_progress` (active this turn) · `committed` (landed, commit hash in the note) · `partially-done` (the note names what moved and what parked) · `done` |
| Issue — `.llms/issues/issue-<stamp>.md` | `status` (the rollup), `captured`, `updated`, `summary`, `count` (items actually listed) | the rollup mirrors its items: `open` · `considering` · `resolved` · `wontfix` | `open` (work to do, nobody working it) · `considering` (a decision parked with a named trigger — act only when it fires) · `resolved` (landed, the resolution names the evidence) · `wontfix` (the owner declined; the trigger records when to reopen) |
| Roadmap — `.llms/roadmap/<name>.md` | `status`, `updated`, `references` (source issues/plans) | `living` (updated in place, stamped) | per entry: `parked (unlock: …)` · `taken (plan-<stamp>)` · `opened (issue-<stamp>)` · `landed (commit/plan)` |
| Handover — `.llms/handover/handover-<stamp>.md` | `status`, `captured` (from the filename stamp), `plan`, `wave` — a key the body cannot name is omitted, not guessed | `done` (the session closed its work) · `partial` (compacted mid-work; the open threads named) | — |

Shared conventions: `updated` is the last time the file's claims were
true (a status flip without a fresh `updated` is a stale file); `count`
on an issue matches the items actually listed; an archived file keeps
its last status — the archive note carries where live content went.
Audits use the same vocabulary as issues at document level (`resolved` /
`open`) with `updated` and `reference`.

## Plans (`.llms/plans/plan-YYYYMMDD_HHMM.md`)

A plan is a contract, written only on the owner's request. Shape:
frontmatter (`status: awaiting-approval` → in progress → `done`,
`updated`, `phases:` with `id`/`title`/`status`/`note`), then Objective,
Current state (verified file:line facts — every claim checked in the
tree first), Decisions (dated, each with its rejected alternatives),
a shape/enforcement table, Execution rules, Phases (each with its own
validation line and suggested commit message), Test matrix, Explicitly
out of scope.

Plan quality rules: a plan claiming `awaiting-approval` carries no
placeholder — every decision is either made and dated or explicitly
asked, never a `TBD`; each phase's validation is objectively checkable
(a command, a probe, a count — not "works correctly"); a decision
records its consequence, the cost the pick carries, not only why the
alternatives lost. For code plans, sequence phases the safe order —
contracts and types first, then implementations, then callers, then
tests, then cleanup — and the riskiest phase names its undo (the
revert or the in-place edit that backs it out).

Execution rules (the owner's standing template — mirror it in every
plan): execution waits for the owner's explicit go-ahead per phase
("continue with phase N"); atomic commit per phase with the plan-named
message; self-serve commits allowed during the plan's working session;
after the last phase, stop and recommend the closing commit message —
never commit it unasked. The final-validation phase and the test matrix
are mandatory; E2E is written only when relevant and recorded n/a
otherwise.

Project policies are the owner's, not this skill's: anything that
shapes the code — backward compatibility, migration policy (in-place
edits vs additive alters), database resets, deprecation windows — is
restated in the plan's execution-rules block only as the owner's
standing rules grant it. The safe defaults when nothing was said:
migrations are additive and forward-only, no destructive operation
(reset, drop, key rotation) runs without explicit consent in the turn,
and compatibility is kept until the owner waives it. A plan that
silently assumes "early stage" or grants itself a reset is wrong.

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

Verify before writing, then verify adversarially: extract every claim
the item makes, check each against the tree, and before repeating a
claim — yours or an archived handover's — ask what would make it wrong
and go look. A stale finding is worse than a missing one: the costliest
reconciliation errors are claims repeated from an archived handover
whose contradicting guard sat one search away. An audit record in
`.llms/audit/` is the format when the finding set is large.

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

## The docs-sync rule and the closing sweep

A change that lands a behavior lands its documentation in the same turn
and the same commit. Dispatch: a contract or invariant → the rules
document; a reasoning/decision/library record → the architecture
document; a shipped surface (endpoint, job, background delivery, guard
rule, settings key) → the endpoint reference, the API client collection
the repository keeps, and `docs/`. The index's rows move in the same
commit — plans gained or archived, issues opened or closed, pointers
(`Newest:`) updated. Outdated prose is a defect of the change, not a
follow-up.

**The closing sweep is mandatory** — every time a task or a plan phase
completes (and again at a plan's final-validation phase), walk this
checklist and sync each line the work touched, in the same commit:

1. **The plan itself** — frontmatter `status` and the phases' `status` /
   `note` lines; a completed plan archives and its index rows move.
2. **Issues** — every item the work touched flips its status line with
   the date and the evidence; a rollup whose items are all terminal
   archives (re-homing live items first).
3. **Roadmap** — an entry a plan took, or whose unlock just fired, gets
   its `status`/`updated` line moved (taken / opened as an issue); the
   document's frontmatter `updated` stamps with it.
4. **`docs/`** — sweep every human-facing document whose subject the
   diff touched: the endpoint/API references, the behavior and feature
   guides, the operations and deployment runbooks, the response-shape
   docs, the user-facing guides. A `docs/` claim the diff made stale is
   the diff's defect. When nothing applies, the n/a is recorded, not
   assumed.
5. **The core documents** — a contract → `rules.md`, a decision or
   library record → `architecture.md`, the map rows → `index.md`.
6. **The handover** — written at the plan's close (mandatory), not left
   to the next session.

The sweep is a checklist, not a vibe: each line is either synced or
explicitly recorded n/a — silence is not a verdict.

## Markdown mechanics

- **No word-wrapping.** Write each paragraph, list item, and table row
  as a single line — the soft wrap is the editor's job, not the file's.
  Never reflow or hard-wrap a document whose lines already run long, and
  never let a formatter introduce mid-sentence breaks: a wrapped line
  makes every diff a noisier one and every `grep` a two-line problem.
  One sentence of diff shows as one changed line.

## Mechanical traps

- Never batch multiple `edit_file` calls to the same file in one
  parallel block — the writes race and one edit is silently lost. Same
  file: sequential, one `old_string` at a time, verified after each.
- Close every ledger batch with a count check (`grep -c` on the status
  lines) and a dangling-reference grep after every move.
- Stage explicit paths; never `git add -A`.

## Reference exemplars

Two kinds live here: the **snapshots** — real ledger documents whose
shape is the lesson — and the **cards** — one-page technique guides.

| File | Kind | The shape it shows |
| --- | --- | --- |
| `references/plan-example.md` | snapshot | A full plan: frontmatter phases, verified current state, decisions with rejected alternatives, the execution-rules block, per-phase commits, matrix, out-of-scope |
| `references/issue-example.md` | snapshot | An issue rollup: frontmatter rollup, per-item statuses with triggers, the settled-records section |
| `references/roadmap-example.md` | snapshot | A roadmap document: frontmatter, per-entry `parked (unlock)` lines, evidence, first moves, source stamps |
| `references/handover-example.md` | snapshot | A handover: frontmatter convention, what the session did, the commits, what stayed open |
| `references/adr-example.md` | card | The decision record: context, decision, consequences both sides, alternatives with evidence-backed rejections; scale — full ADR vs compact record |
| `references/verification-example.md` | card | The adversarial claim check: extract, tree-check, break — the verdicts (verified / stale / unfounded / contradicted) and what each writes |
| `references/refactor-sequence-example.md` | card | The safe sequence (contracts → implementations → callers → tests → cleanup), the affected-files table, the named undo |
| `references/acceptance-criteria-example.md` | card | Requirements vs constraints vs guidelines; Given-When-Then criteria; the measurable bar; non-goals as a first-class section |
| `references/docs-tone-example.md` | card | The human-docs tone: lead with the why, one idea per paragraph, GFM and admonitions earned, examples over descriptions, no internals |
