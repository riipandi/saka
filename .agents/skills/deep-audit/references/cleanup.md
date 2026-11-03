# Cleanup and Dedupe

Goal: shrink the surface area that must be maintained without changing behavior that is still needed.

## What to look for

- **Dead code**: functions/classes/modules/routes/handlers with no callers; unreachable branches; unused parameters/fields/imports; code behind flags that are always on/off.
- **Obsolete/legacy**: old API versions with no clients, finished data migrations, compatibility code for unsupported versions, unnecessary polyfills, deprecated endpoints past their sunset date.
- **Shims/backward compatibility**: adapters, aliases, "temporary" wrappers, re-exports, fallbacks to old behavior. Identify the consumers and whether they still exist.
- **Test shims**: mocks/stubs/fakes duplicating production logic, duplicate test helpers, unused fixtures, permanently skipped tests, tests that only exercise mocks, stale snapshots, piles of overlapping test utilities.
- **Stale artifacts**: old comments/TODO/FIXME, docs out of sync with code, config/env vars nothing reads, unused dependencies, dead scripts/CI jobs, generated files no longer used, old feature flags.
- **Duplication**: copies of logic, constants, types, queries, and validation schemas across places.

## Verify before deleting

1. Search static references (grep/LSP/find-usages), including names used as strings.
2. Check dynamic usage: reflection, DI containers, route/plugin registries, serialization (field names), job schedulers, templates, config, SQL/ORM, deploy scripts.
3. Check external consumers: public libraries, APIs used by other clients, webhooks, dashboards, production data still depending on it.
4. Run tests with coverage; see whether the code ever executes. Use production telemetry/logs as usage evidence when available.
5. Read the history (`git log -S`, blame) for the reason it exists.

## Confidence levels

- **High** (no static or dynamic references, tests green without it) -> propose deletion directly.
- **Medium** (possible dynamic/external use) -> ask the user with options: delete / deprecate with warning first / keep. Recommend based on the evidence.
- **Low** -> do not delete; record as an open question.

## How to execute

Separate batch from bug fixes; one category per commit; run tests and build after each batch; state exactly what was removed and the evidence. After deleting, clean up leftovers (imports, config, docs, now-unused dependencies).
