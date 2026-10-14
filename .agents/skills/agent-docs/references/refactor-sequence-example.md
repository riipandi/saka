# Reference card — the safe sequence (from `refactor-plan`)

A multi-file change — a refactor, a contract shift, a sweep — is sequenced so the tree is consistent at every phase boundary, not just at the end.
Each phase leaves the build green or the plan says it does not and names why that is safe.

## The sequence

1. **Contracts and types first** — the interface, the schema, the proto, the migration. The tree compiles against the new shape with the old behavior intact (or a named temporary shim), so every later phase is mechanical.
2. **Implementations second** — the bodies behind the new contracts, inside-out: the deepest package first, no caller moved yet.
3. **Callers third** — switch the call sites, one module at a time; each is a small, reviewable commit.
4. **Tests fourth** — re-pointed at the new shape; the negative checks (the failure paths) move with the positives.
5. **Cleanup last** — the old shape deleted, the shims dropped, the deprecated helpers removed. Nothing called "old" survives the plan.

## The rules

- **One direction.** No phase reaches back into an earlier phase's files — if it does, the phases were cut on the wrong seam.
- **Named undo.** The riskiest phase carries its recovery: the revert command, the in-place edit that backs it out, the feature flag to flip. "Git can undo it" is not a plan — which commit, which files, said before the risk is taken.
- **Verification between phases** — the phase's own command proves the boundary before the next starts, and a red phase stops the plan rather than being committed alongside the fix for it.
- **The affected-files table is honest** — every file the plan will touch is listed with its change type (create/modify/delete) and what it blocks or is blocked by; a file discovered mid-execution is a signal the Current state was not verified.
