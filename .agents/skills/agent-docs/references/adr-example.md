# Reference card — the decision record (from `create-architectural-decision-record`)

A decision record lives in the architecture document (or beside the code it governs). A template ADR's discipline, distilled to the ledger's scale — a full ADR for every choice would be ceremony; the shape below is the minimum a reader needs when the "why" outlives the conversation.

## The shape

```markdown
### <The decision, stated as the state of the world>

Context: <the forces — the constraint that forced a choice, the problem, the deadline. One short paragraph.>

Decision: <what was picked, and the one reason that survived the alternatives.>

Consequences:
- (positive) <what the pick buys — the benefit, named concretely>
- (negative) <what the pick costs — the trade-off, the debt, the capability given up. A decision with no named cost was not examined.>

Alternatives considered:
- <alternative> — rejected: <the reason, specific to this tree, not "considered and rejected">
```

## The rules

- **Both sides of the ledger.** A record that lists only benefits was written by someone selling. The negative consequence is the part a future reader trusts.
- **Rejection reasons are evidence, not vibes** — "the engine refuses X", "two sources of truth", "an outage deserves the retry budget" — never "we decided against it".
- **Dated.** A decision's truth has a birthday; the tree moves.
- **Superseding is explicit.** A new decision names the record it replaces; the old record gains `superseded_by` rather than being rewritten (history is the audit trail).
- Scale: repository-wide architectural choices earn a full ADR document; feature-level picks earn the compact shape above inside the plan's Decisions section; micro-choices need no record at all — the code comment carries them.
