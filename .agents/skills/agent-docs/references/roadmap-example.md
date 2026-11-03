---
status: living
updated: YYYY-MM-DDTHH:MM:00+07:00
references: <the issue and plan stamps the entries came from>
---

# Roadmap — future improvements

> Example roadmap document — generic names throughout. Parked
> improvements and delivery analyses: not plans, not issues. An entry
> here carries its own status line, the unlock that fires it, the
> verified evidence, and a first move. When an unlock fires, the entry
> re-enters the issue ledger or goes straight to a plan.

## <The theme, e.g. Protocol & federation>

### RI-1 — <the improvement, as a noun phrase>
*(from issue-<stamp> <item id>)*

- status: parked (unlock: <the named event>) · updated YYYY-MM-DD

<What and why: the capability gap, the constraint that makes it non-urgent, and the evidence the tree can carry it — file:line for the seam it would use, re-verified on the date stamped.>

**Unlock:** <the event. The unlock is a fact about the world, not a
wish — name who or what fires it.>
**First move:** <what the plan or task would touch first, in one line.>

### RI-2 — <the improvement with an owner deferral>
*(from issue-<stamp> <item id>)*

- status: parked (owner-deferred YYYY-MM-DD; unlock: <the named event>)
  · updated YYYY-MM-DD

<What and why. A deferral is a decision, so it carries the date and the owner's word — an entry parked by decision reopens by decision.>

**Unlock:** <the event>. **First move:** <the first line of work>.

## <The theme, e.g. Dependencies & toolchain (upstream-gated)>

### RI-3 — <the upstream-gated cleanup>
*(from issue-<stamp>)*

- status: parked (unlock: <the upstream release or tag>) · updated
  YYYY-MM-DD

<What is pinned and why it must wait: the indirect dependency, the missing module, the upstream bug — each with the file:line of today's state and the workaround that holds until then.>

**Unlock:** <the release, the tag, the fix — checkable by command>.
**First move:** <the mechanical step, when it is one — the tidying, the import-path change, the workaround to delete with its guard>.
