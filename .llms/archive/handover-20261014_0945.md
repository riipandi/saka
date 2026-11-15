---
status: done
captured: 2026-10-14T09:45:00+07:00
plan: plan-20261014_0930
wave: —
---

# Handover — 2026-10-14 09:45

## What this session did

**The issues ledger reshuffle** (owner instruction, executed ahead of
plan-20261014_0930's phases): the five aging issue rollups archived flat —
`.llms/archive/issue-20261006_0050.md`, `issue-20261008_1705.md`,
`issue-20261009_0127.md`, `issue-20261009_1915.md`,
`issue-20261012_1605.md` — each annotated with where its live items went.
The still-relevant items were re-homed first, per the owner's rule
("move what is still relevant to open into new issues"):

- `issue-20261014_0935.md` — the OIDC conformance and platform-direction
  residues (8 items from the 2026-10-06 rollup: the hosted cert run,
  RFC 8707 resource indicators, internal MCP servers, DCR, Form Post OP,
  the RP rehearsal, RP-side logout, the compose comments).
- `issue-20261014_0936.md` — the connect-go v2 upstream-gated residues
  (2 items: buf's indirect v1 module, validate/otelconnect with no /v2).
- `issue-20261014_0937.md` — the `vp run` Linux-container stack overflow,
  re-homed unchanged from the 2026-10-12 issue.

**plan-20261014_0930 updated** (`0cff9d3a`, then the race-repair commit):
its phase 2 no longer archives the two issues — the archive landed with
this reshuffle — so the plan's remaining work is phase 1 (the boundary
records in rules/architecture/docs), phase 2 (handover + index sync),
phase 3 (final validation). The triage behind the carve-out: `1705` and
`0127` archived whole (every item terminal — `wontfix` by owner decision,
recorded in the plan's D1/D3); `0050` kept eight live items; `1915` two.

## Commits (this slice)

- `docs(issues): re-home the live items of the aging rollups` — the three
  new issue files.
- `docs(issues): archive the five settled rollups` — the five moves +
  index recap.
- `docs(plans): the ledger close lands ahead of the decision plan` — and
  the race repair (see below).

## What stayed open / the trap

- **Parallel `edit_file` to the same file races and silently drops
  edits.** Two of this slice's commits landed with earlier hunks lost
  (the index recap table; the plan's shape-table row and frontmatter
  phase title). Found by the dangling-reference grep + re-read; repaired
  sequentially. Every future same-file edit sequence stays sequential.
- The index's "Finding history fast" section had pre-existing duplicated
  fragments — cleaned in the same pass.
- plan-20261014_0930 remains `awaiting-approval` — its phases wait for
  the owner's "Lanjut fase 1". The live issues directory now holds only
  the three re-homed files; the index's recap, plans rows, and pointers
  agree with the tree.
