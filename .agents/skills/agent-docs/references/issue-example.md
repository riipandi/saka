---
status: open
captured: YYYY-MM-DDTHH:MM:00+07:00
updated: YYYY-MM-DDTHH:MM:00+07:00
summary: "One sentence: what was found, where it lives, and what the rollup carries. The count matches the items actually listed."
count: 3
---

# <The finding area, as a title a scanner can triage on>

> Example issue document — generic names throughout. One file per
> capture date; a finding that outlives the turn lands here the moment
> it is found.

## <Finding 1 — the work item>

- status: open
  area: backend/<package> or frontend/<area>
  summary: <What is wrong or missing, with the file:line that proves it — the evidence is in the item, not in the conversation.>
  plan: <the plan document this item came from or must honor, or: none yet>
  trigger: <the event that makes this the next thing to do — who acts, what unblocks it>
  note: <the history that matters — when it was parked, what was verified and when. Omitted when there is nothing to add.>

## <Finding 2 — the parked decision>

- status: considering (trigger: <the named event>)
  area: <area>
  summary: <The choice parked, the options as they stood, and where the analysis lives.>
  trigger: <the event; act only when it fires — a trigger without a named event is an open item wearing a disguise.>

## <Finding 3 — the settled record>

- status: resolved (date, the plan or commit reference)
  area: <area>
  summary: <What the finding was.>
  resolution: <What landed, with the evidence; what it opened, if anything — a resolution that opens work names the new item.>
  trigger: None — closed. <Or the condition that would reopen it, when one exists.>

## A wontfix item (when one exists)

- status: wontfix (owner decision, dated)
  area: <area>
  summary: <The finding.>
  mitigation: <What makes living with it safe — the mechanism that bounds the risk.>
  trigger: <The condition that would reopen it. A wontfix without a named trigger is a permanent record; with one, it is a parked decision.>
