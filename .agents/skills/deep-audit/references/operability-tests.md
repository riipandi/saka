# Operability and Test Quality

## Operability

- Logging: right levels, structured, correlation/request IDs, no secrets/PII, not noisy in hot paths.
- Metrics and tracing: key metrics (latency, error rate, saturation) present; label cardinality controlled.
- Error handling: errors not swallowed; user-facing messages do not leak internals; errors map to correct status codes.
- Resilience: timeouts, retries with backoff + jitter only for idempotent operations, circuit breakers, bulkheads, backpressure, graceful degradation.
- Configuration: validated at startup (fail fast), safe defaults, secrets from a secret manager/env, no environment-specific config in code.
- Honest health/readiness checks; graceful shutdown.
- DB migrations: forward/backward safe, no long locks on big tables, compatible with rolling deploys.
- Build and deploy: reproducible, pinned dependencies, minimal images, non-root execution.

## Test quality

- Coverage of critical flows and failure paths, not just a coverage percentage.
- Flaky tests (time, ordering, network, shared state) -> stabilize or quarantine with a note.
- Over-mocking: tests that pass while real behavior is broken; replace with integration tests at important boundaries (real database via container rather than mocks).
- Tests asserting implementation instead of behavior (brittle under refactor).
- Slow tests, tests without meaningful assertions, tests depending on each other.
- Missing tests for concurrency, authorization (cross-user/tenant access), and hostile input validation.
