# Reference card — acceptance criteria (from `create-specification` / `prd`)

A phase's validation line and a plan's test matrix are acceptance criteria: statements a stranger can run and agree with.
Three habits from the specification discipline keep them honest.

## Requirements, constraints, guidelines — kept distinct

- **Requirement** (must hold; testable): "the rotation aborts on the first decrypt failure leaving zero writes."
- **Constraint** (bounds the how; not negotiable): "no migration may be altered after it shipped to a shared environment."
- **Guideline** (the default; deviate with a reason): "prefer the smallest honest fix over the systemic one."

A document that blurs these makes every disagreement unresolvable — the reader cannot tell a rule from a preference.
Label which kind a line is when the prose does not make it obvious.

## The Given-When-Then shape for behavioral criteria

```markdown
- **AC-1**: Given a seeded database with a sealed credential, when the rotation command runs with the correct current key, then every sealed row decrypts under the new key and the old key opens nothing; the command prints the per-surface counts.
- **AC-2**: Given a wrong key, when the rotation's dry run executes, then it exits non-zero, names the first failing surface, and the tree is untouched.
```

Given (the prestate, seeded and specific) — When (the action, the exact command or event) — Then (the observable, not the internal state).
"Then" observable means a test could assert it without reading minds.

## The measurable bar

Vague is unusable: "fast", "robust", "handles edge cases", "works correctly".
Concrete is a gate: a number, a command, a probe, a count — "the search answers within 200 ms over the seeded corpus", "lint reports zero findings", "the probe prints healthz 200".
A criterion a test could not assert is a wish, and wishes do not gate a phase.

## Non-goals are first-class

What the work will NOT do sits in its own section with a reason per line — protecting scope is the point: a named non-goal cannot be scope-crept back in by accident, and a future reader who expected it finds the decision instead of a silence.
