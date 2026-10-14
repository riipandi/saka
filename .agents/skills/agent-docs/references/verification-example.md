# Reference card — the adversarial claim check (from `doublecheck`)

Before a claim enters the ledger — an issue item, a plan's current-state bullet, an architecture record — it passes three layers.
The web-search pipeline of the source skill becomes a tree-check pipeline here: the source of truth is the working tree, not the open web.

## Layer 1 — extract the claims

Read the finding and pull out every statement that asserts something
checkable, categorized:

| Category | In a code ledger |
| --- | --- |
| Existence | "the engine supports X", "the option exists" — a named symbol at a named location |
| Behavior | "the gate rolls back the revocation", "the limiter covers module routes" |
| Absence | "nothing validates Y", "never mounted", "no tests cover it" |
| Attribution | "the audit found", "the plan decided" — the record says what the claim says |
| Temporal | "still true today", "since commit X" — the claim's freshness |

Absence claims are the highest-risk category — proving something is not there takes a search strategy, not a glance.

## Layer 2 — check each claim against the tree

For each claim: open the named location, or search for the counter- evidence the claim denies.
A claim without a file:line in the final text was not checked.
Repeating a claim from an archived document is re-verifying it, not citing it — the tree moves under old documents.

## Layer 3 — the adversarial pass

Assume the claim is wrong and try to break it:

- What would make this wrong? (a guard added since, a default raised, a rename)
- Is the claim about a snapshot of the tree that has already moved?
- If the claim is an absence — did I search for the thing's second name?
- Internal consistency: does this text contradict the record it cites?

## The verdicts

| Verdict | Meaning | Action |
| --- | --- | --- |
| verified | file:line evidence in hand | write it with the evidence |
| stale | the tree moved; the claim's subject changed | rewrite the claim, cite the new state |
| unfounded | no evidence either way | do not write it — check again or drop it |
| contradicted | found the guard/record the claim denies | flip the finding; the contradiction is the news |

A claim that survives all three layers is written with its evidence; a claim that does not is either corrected or dropped — never softened ("might", "possibly") into the ledger. Hedged claims age into wrong ones.
