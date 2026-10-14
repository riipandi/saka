---
status: awaiting-approval
updated: YYYY-MM-DDTHH:MM:00+07:00
phases:
  - id: 1
    title: <the contract or schema change>
    status: pending
    note: —
  - id: 2
    title: <the implementation>
    status: pending
    note: —
  - id: 3
    title: Docs sync, ledger close, handover
    status: pending
    note: —
  - id: 4
    title: Final validation
    status: pending
    note: —
---

# <Verb the outcome> — <the one-sentence scope>

> Example plan document — generic names throughout. The shape is the
> lesson; substitute the repository's real surfaces.

## Objective

<What this plan settles or ships, in three sentences: the finding or request that caused it, what the tree will look like after, and the one boundary the plan does not cross.>

## Current state (verified in the tree, YYYY-MM-DD)

- **<Claim 1>** — `<path/file.go:LINE>` names the code as it stands today, quoted or paraphrased precisely.
- **<Claim 2>** — the absence or behavior the plan responds to, with the search that proved it (absence claims name their search).
- **<Claim 3>** — the decision or record this plan extends or closes, with its reference.

## Decisions

- **D1 (date): <the pick, stated as fact>.** <The reason that survived the alternatives — specific to this tree.> The cost the pick carries:
  <the trade-off, named>.
  Rejected: <alternative> — <the evidence-backed reason it lost>.
  Rejected: <alternative> — <the reason>.
- **D2 (date): <the second fork, resolved>.** <Reason.> Rejected: <the alternative and its reason>.

## Shape and enforcement points

| What | Where | The record |
| --- | --- | --- |
| <The contract> | `rules.md`, <the section> | <what the next reader must know> |
| <The reasoning> | `architecture.md`, <the section> | <D1/D2's record> |
| <The human-facing bit> | `docs/<the doc>`, <where> | <the sentence the reader needs> |
| <The closed finding> | the issue → archived | <the owner's pick, the date, this plan> |

## Execution rules

- **Atomic commit per phase** is authorized; each phase's heading names its suggested commit message; self-serve commits are allowed during this plan's working session.
- **Execution waits for the owner's explicit "continue with phase N".** When the phases have all landed, the agent stops and recommends the closing commit message with its paths — it does not commit the closing state on its own.
- <Project policies restated as the owner grants them — migration policy, reset consent, compatibility windows. Absent a grant: additive migrations, no destructive operation, compatibility kept.>
- **The owner's parallel work is untouchable:** <the files and directories the owner named for this working session>.
- **Per-phase validation runs before each phase's commit.**
- **E2E only when relevant** — recorded n/a otherwise, never skipped silently.

## Phases

1. **<The contract or schema change>** — <what lands>. Validation: <the exact command or probe that proves it>. *Commit: `<type>(<scope>): <the message>`.*
2. **<The implementation>** — <what lands>. Validation: <the command>; the edge cases from the matrix rows it touches. *Commit: `<type>(<scope>): <the message>`.*
3. **Docs sync, ledger close, handover** — the records land (rules / architecture / docs/), the issue flips and archives, the index rows move, the handover carries the decisions. Validation: the docs' claims diff-checked against the landed code; the index agrees with the tree. *Commit: `docs: <the message>`.*
4. **Final validation** — the matrix below, run over the tree as it stands. Validation: the matrix green end to end. *Commit: none if green; the ledger note rides the docs phase.*

## Test matrix

| What | How | Green |
| --- | --- | --- |
| Unit | `go test ./<the touched packages>/ -count=1` | The new behavior + its failure paths |
| Integration | <the seeded-DB or harness run> | <the observable> |
| Lint | `task lint` | 0 issues |
| Full suite | `go test ./...` (+ the debug/tag variant), sequential | All green |
| Composition root | <the built-binary probe against a running service> | <the endpoint answers; the log clean> |
| E2E | <relevant scenario> or — recorded n/a with the reason | — |
| Contract drift | <the stale-check for the wire/serialization, when one exists> | — |

## Explicitly out of scope

- **<The tempting neighbor>** — <the reason it stays out>.
- **<The deferred mechanism>** — <the trigger that would open it>.
