# Correctness

Goal: confirm each flow behaves as expected, including with odd input or failing dependencies.

## Determining "expected"

Order of truth sources: spec/PRD/ADR -> API docs -> existing tests -> current code behavior. If sources conflict or are missing, ask the user what the correct behavior is. Do not treat current behavior as correct by default.

## Per-flow checklist

- Happy path works end to end, including side effects (DB, events, email, cache).
- Input validation: boundaries (zero, negative, maximum, empty, null), format, encoding/Unicode, very large input, wrong types.
- Error paths: what happens when a dependency times out, partially fails, or returns unexpected data? Are errors swallowed? Is partial state left behind?
- Idempotency and retries: repeated requests (client retries, duplicate webhooks, message redelivery) do not duplicate effects.
- Data consistency: invariants across tables/services, non-atomic multi-step operations, orphan records, soft-deleted rows leaking into queries.
- State machines: illegal transitions, states that can get stuck forever.
- Time: time zones, DST, rounding, expiry, clock skew, date comparisons.
- Numbers and money: floating point for currency, overflow, rounding, currency handling.
- Pagination, sorting, filtering: stable ordering, off-by-one, duplicate/missing rows while data changes.
- API contracts: response backward compatibility, status codes, consistent error format, optional vs required fields.
- Migrations and seeds: re-runnable, rollback available, old data stays valid.
- Configuration: safe defaults, behavior when config/env is missing or wrong.

## Commonly missed bug patterns

Wrong comparison (equality vs identity), shared mutation of arguments, closures capturing loop variables, null checks after use, errors wrapped and losing their type, cleanup blocks overwriting the original error, stale caches, mutable default parameters, silent integer truncation, partial-failure handling in batch operations.
