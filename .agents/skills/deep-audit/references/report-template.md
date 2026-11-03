# Audit Report Template

Save the report to `.llms/audit/audit-YYYYMMDD_hhmm.md` (repository root; local time of capture, 24-hour clock; read the clock with a command, never guess).

**Language:** the report file is always in English, including frontmatter. A summary shown in chat follows the user's current language (translate prose; keep identifiers, paths, and code unchanged).

## Frontmatter

Start the file with this YAML block. Keep every key; use `null` or `[]` when a value does not apply. Update it on every edit.

```yaml
---
title: 'Audit: <feature name | whole system>'
status: draft # enum, see "Status values"
captured_at: 2026-01-31T14:05:00+07:00 # ISO 8601 with UTC offset; when the audit snapshot was taken; set once, never changed
updated_at: 2026-01-31T14:05:00+07:00 # ISO 8601 with UTC offset; refresh on every edit
completed_at: null # ISO 8601 when status becomes completed or cancelled, else null
scope:
  type: feature # feature | system
  feature: '<feature name or null>'
  paths: [] # main directories/modules covered
  exclusions: [] # explicitly out of scope
stack:
  languages: []
  frameworks: []
  datastores: []
repo:
  branch: '<branch or null>'
  commit: '<short SHA audited, or null>'
  working_tree_clean: true # false if uncommitted changes existed at capture time
environment:
  executed: [] # what was actually run, e.g. [unit-tests, linter, dependency-scan, race-detector]
  not_run: [] # what could not be run, with reason in "Coverage and limitations"
baseline:
  build: not_run # pass | fail | not_run
  tests: not_run # pass | fail | not_run
  lint: not_run # pass | fail | not_run
  dependency_scan: not_run # pass | fail | not_run
  secret_scan: not_run # pass | fail | not_run
  race_detector: not_run # pass | fail | not_run | not_available
findings:
  total: 0
  by_severity: { critical: 0, high: 0, medium: 0, low: 0, info: 0 }
  by_status: { open: 0, fixed: 0, deferred: 0, wont_fix: 0, false_positive: 0 }
  verified: 0 # findings with a failing test, repro, or measurement
  unverified: 0
cleanup_candidates: 0
optimization_opportunities: 0
open_questions: 0
tests_added: 0
standards: [] # e.g. [OWASP Top 10:2021, OWASP API Security Top 10 (2023), ASVS]
generated_by: deep-audit
---
```

### Status values (report `status`)

| Value         | Meaning                                                                                                  |
| ------------- | -------------------------------------------------------------------------------------------------------- |
| `draft`       | Audit in progress (Phases 0-4); findings may change.                                                     |
| `in_review`   | Report complete; waiting for the user's decisions or fix-plan approval.                                  |
| `approved`    | User approved the fix plan (or a subset); fixes not started.                                             |
| `in_progress` | Fixes or cleanup are being applied.                                                                      |
| `completed`   | Approved work is done and verified; remaining items are recorded as deferred or won't-fix.               |
| `blocked`     | Cannot proceed (missing access, unanswered decision, broken environment); state the blocker in the body. |
| `cancelled`   | Audit abandoned by the user; keep the file for history.                                                  |

Typical flow: `draft` -> `in_review` -> `approved` -> `in_progress` -> `completed`. `blocked` and `cancelled` can occur from any state.

### Finding status values (per finding)

| Value            | Meaning                                                              |
| ---------------- | -------------------------------------------------------------------- |
| `open`           | Reported, not yet fixed or decided.                                  |
| `fixed`          | Fixed and verified (regression test or re-measurement passes).       |
| `deferred`       | Valid, intentionally postponed; give the reason and a trigger/owner. |
| `wont_fix`       | Valid but accepted risk; record who accepted it and why.             |
| `false_positive` | Investigated and not a real issue; keep it with the explanation.     |

## Body structure

Omit sections that do not apply; do not pad them with filler.

```markdown
# Audit: <feature name | whole system>

## Executive summary

- Overall condition in 2-3 sentences.
- Findings per severity (must match frontmatter).
- The 3 things to fix first.

## Scope and flow inventory

Flows audited (e.g. "register -> verify email -> login"), trust boundaries, assumptions.

## Baseline

Build/test/lint/dependency scan/secret scan/race detector: results, test count, flaky tests, anything that could not be run.

## Findings

| ID   | Severity | Category       | Location          | Title | Evidence | Status |
| ---- | -------- | -------------- | ----------------- | ----- | -------- | ------ |
| F-01 | critical | security (A01) | path/file.ext:123 | ...   | verified | open   |

### F-01 <title>

- **Category**: security / race / leak / defect / performance
- **Location**: `file:line`
- **Problem**: what is wrong
- **Scenario**: concrete steps that trigger the failure/exploit
- **Impact**: who/what is affected
- **Evidence**: test/repro/profile (path or short output); `verified` or `unverified`
- **Fix**: specific change + trade-offs
- **Confidence**: high / medium / low
- **Status**: open / fixed / deferred / wont_fix / false_positive (with note)

## Optimization opportunities

Problem -> measurement before -> proposal -> expected benefit -> risk -> effort (S/M/L)

## Cleanup candidates

| Item | Type (dead/legacy/shim/stale/dup) | Evidence of non-use | Confidence | Proposal | Status |

## Decisions log

Decisions the user made (date, question, chosen option, rationale).

## Coverage and limitations

What was audited, what was not, and why.

## Open questions

Questions with options (including a recommended one) and a free-input option.

## Change log

- <timestamp>: status draft -> in_review (short note)
```

## Severity scale

Use lowercase values in tables and frontmatter counts.

- **critical**: exploitation/data loss/wide downtime with no hard prerequisites (RCE, auth bypass, SQL injection, data corruption).
- **high**: serious impact with reasonable prerequisites (IDOR, a race that corrupts balances, a leak that eventually takes the service down).
- **medium**: limited impact or needs special conditions.
- **low**: minor, hardening, quality.
- **info**: notes/suggestions without direct risk.
