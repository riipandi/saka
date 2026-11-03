---
name: deep-audit
description: >
  Deeply review and test code for correctness, races, leaks, defects, security
  issues, performance problems, and stale or unnecessary code. Use for audits,
  code or security reviews, hardening, pre-release checks, bug hunting,
  optimization, or cleanup, even when phrased indirectly or in another language.
  An optional feature name narrows the scope; otherwise, audit the whole system.
metadata:
  argument-hint: '[feature-name]'
---

# Deep Audit

An evidence-based, end-to-end audit: find real problems (not a generic list of suggestions), verify them with tests or code traces, then clean up the codebase after the user approves scope and key decisions.

This skill is stack-agnostic. Detect the language, framework, and database from the repository and use that ecosystem's native tools. Examples in the reference files are illustrative only.

## Input

**Optional argument: feature name** (e.g. `checkout`, `auth`, `report-export`). Take it from the skill argument, the text after the skill name, or `$ARGUMENTS` if the runtime provides it.

- **Feature name given** - audit that feature and every flow touching it (entry points, services, storage, jobs/queues, external integrations, consuming UI/API). Follow dependencies; do not stop at one directory.
- **No argument** - audit the whole system. Start with the feature inventory (Phase 1), then ask the user to pick priorities if the system is large.
- **Name matches nothing in the code** - do not guess. Offer the closest candidates as choices.

## Language rules

- **Skill-internal content, and the report file saved under `.llms/audit/`**: English (including frontmatter keys and values).
- **Everything said in chat** (summaries, findings overview, questions, progress notes): the language the user is currently writing in. If the user switches language, follow.
- Technical identifiers, code, file paths, and tool output stay as-is regardless of language.

## Working principles

1. **Evidence before claims.** Every finding needs a location (`file:line`), a concrete failure scenario, and ideally a test, repro, or measurement. Findings without evidence are labeled "hypothesis", not "defect". Reports full of false positives destroy trust.
2. **Read before changing.** Phases 0-4 are read-only on production code (adding tests is fine). Code changes start only after the user approves the plan in Phase 6.
3. **Verify before deleting.** "Unused" code is often reached via reflection, configuration, dynamic routes, scheduled jobs, or external consumers. Delete only with evidence.
4. **Ask when unsure; never assume.** See the confirmation protocol below.
5. **Prioritize by impact.** Rank by severity x likelihood x blast radius, not by discovery order.
6. **Stay in scope.** Out-of-scope issues go into the recommendations list; do not fix them on the side.

## Confirmation protocol

Stop and ask when you hit: ambiguous scope, system design or architecture decisions, trade-offs that change behavior (breaking changes, schema/data migrations), deletions with inconclusive evidence, or changes touching high-risk areas (authentication, payments, production data).

Question format:

- Use an interactive question tool when available (e.g. `ask_user_input_v0`, `AskUserQuestion`); otherwise a numbered list.
- Offer 2-4 concrete options, mark exactly one **(Recommended)** with a one-sentence reason, and state each option's trade-off.
- Always include a final **"Other - free input"** option so the user can decide on their own.
- One decision per question; at most 3 questions per turn. Batch small matters instead of interrupting repeatedly.
- Do not ask what can be answered by reading the code or repo.
- Write the questions in the user's current chat language.

## Workflow

### Phase 0 - Scope and context

Determine scope from the argument. Skim README, docs/ADRs, directory layout, dependency manifests, CI config. Summarize in 3-5 lines: stack, architecture, audited feature(s), assumptions. If a scope decision matters (whole system vs part, which environments may be exercised, whether databases or external services may be touched), ask now.

### Phase 1 - Feature and flow inventory

Map entry points (routes, handlers, CLI commands, jobs, event consumers), data flow, shared state, trust boundaries (user input, external services, inter-service calls), and storage. Output a list of flows to audit, e.g. "register -> verify email -> login -> refresh token". For whole-system audits, show the list and ask the user to prioritize if it is too large. Then create the report file with `status: draft` (see Phase 5) and record the flow inventory in it.

### Phase 2 - Automated baseline

Run what already exists before analyzing by hand: build, test suite, linter/type checker, static analyzers, dependency vulnerability scan, secret scan, and the stack's race detector/sanitizers where available. Record results and flaky tests. If something cannot be run (needs credentials, database, external services), record it as a limitation; never skip it silently.

### Phase 3 - Deep audit by dimension

Walk each flow through the dimensions below. Read only the reference files that are relevant.

| Dimension                    | Focus                                                                                                    | Reference                             |
| ---------------------------- | -------------------------------------------------------------------------------------------------------- | ------------------------------------- |
| Correctness                  | Behavior matches spec, edge cases, error paths, idempotency, data consistency, API contracts             | `references/correctness.md`           |
| Concurrency and resources    | Race conditions, deadlocks, TOCTOU, transactions/isolation, memory and handle/connection leaks, shutdown | `references/concurrency-resources.md` |
| Security                     | OWASP Top 10, API Security Top 10, ASVS, secrets, supply chain, data privacy                             | `references/security-owasp.md`        |
| Performance                  | Memory, CPU, DB schema and queries, N+1, indexes, caching, duplicated work, needless complexity          | `references/performance.md`           |
| Code hygiene                 | Dead code, shims, legacy/backward-compat leftovers, duplication, stale config/tests/docs                 | `references/cleanup.md`               |
| Operability and test quality | Logging, metrics, timeouts/retries, configuration, test quality and gaps                                 | `references/operability-tests.md`     |

For each finding record: location, description, failure/exploit scenario, impact, severity, confidence, proposed fix.

### Phase 4 - Verification through testing

Turn important findings into proof:

- Write a test that **fails** because of the bug (regression test), or a repro script for races, leaks, and vulnerabilities.
- Races: run tests under the stack's race detector or sanitizer, plus repeated parallel stress runs.
- Leaks: measure before/after under repeated load (heap profile; counts of threads/tasks, file handles, connections).
- Performance: measure first (benchmark, query plan, profiler). Never claim "faster" without before/after numbers.
- Add tests for critical flows that lack coverage, including failure paths.
  New tests follow the project's conventions and location. Never touch production data; use fixtures or a local database.
  Findings that cannot be reproduced may still be reported, labeled "unverified".

### Phase 5 - Report

Write the report using `references/report-template.md`. It starts with YAML frontmatter (status, timestamps, scope, counts, and other metadata) followed by: executive summary, findings table sorted by severity, per-finding details, cleanup candidates (with evidence), optimization opportunities (with numbers where available), unaudited areas, and open questions.

**Output location and naming**

- Save the report to `.llms/audit/audit-YYYYMMDD_hhmm.md` relative to the repository root (create the directories if missing). The timestamp is the local time the audit was captured (24-hour clock); get it from the system clock (e.g. `date +%Y%m%d_%H%M`), never guess it.
- Create the file early (end of Phase 1) with `status: draft` and fill it in as phases complete, so progress survives an interrupted session.
- If a report for the same scope already exists and the user is continuing it, update that file (keep `captured_at`, refresh `updated_at`) instead of creating a new one. If unsure whether to continue or start fresh, ask. Never overwrite another report.
- Do not edit `.gitignore` or commit the report unless asked. If `.llms/` is not ignored by git, mention it in one line and let the user decide.
- Keep frontmatter and body consistent: counts, `status`, and `updated_at` must be updated on every edit.

**Language**

- The report file is always **English**.
- In chat, give the summary in the user's language: the top findings, the file path, and the decisions needed. If the report is short, it may be shown in full in chat in the user's language.

### Phase 6 - Decisions and fixes

1. Present an ordered fix plan (critical to low), grouped into independently shippable changes. For every decision that needs the user's choice, use the confirmation protocol.
2. After approval, work in small steps. After each step run the related tests; run the full suite at the end. Use one logical change per commit if the repo uses git (do not commit or push unless asked).
3. Keep cleanup in batches separate from bug fixes so they are easy to review and revert.
4. Keep the report file current as work proceeds: set `status` (`approved` once the plan is accepted, `in_progress` while fixing, `completed` when done), update each finding's `status`, refresh the counts and `updated_at`, and fill `completed_at` at the end.
5. Finish with a summary: what was fixed, what was deferred and why, test results before vs after, remaining risks.

## Safety rules

- Do not run exploits or penetration tests against systems the user does not own, or against production. Verify vulnerabilities only in local/test environments.
- Never leak discovered secrets: report location and type only, mask values, and recommend rotation.
- Never run migrations, `DROP`, or other destructive commands without explicit confirmation.
- Changes outside the approved scope become recommendations, not edits.

## Style

Be concise and specific. Avoid generic advice like "add validation" without saying where and why.
