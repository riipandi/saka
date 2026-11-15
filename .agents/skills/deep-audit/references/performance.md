# Performance and Optimization

Core rule: **measure first, optimize second.** Every recommendation carries evidence (profile, query plan, benchmark) or is labeled "analysis-based, not measured". Optimizations that add complexity without measurable benefit are rejected.

## Database: structure

- Missing indexes for columns in `WHERE`/`JOIN`/`ORDER BY`; duplicate or unused indexes; column order in composite indexes; suitable partial/covering indexes.
- Poor data types, nullable columns without reason, normalization/denormalization that does not fit access patterns.
- Missing constraints (FK, unique, check) that force validation into the application and open races.
- Tables growing without an archival/partitioning/retention strategy; bloat; large JSON columns queried frequently.

## Database: queries and access

- N+1 queries (loops issuing a query per item) -> batch/join/preload.
- `SELECT *` on wide tables; fetching then filtering in the application; large `OFFSET` -> keyset pagination.
- Queries inside long transactions, lock contention, queries that cannot use indexes (functions on columns, leading-wildcard `LIKE`, implicit casts).
- Excess round trips; no connection pooling or badly sized pools; prepared statements not reused.
- Verification: query plan analysis (`EXPLAIN ANALYZE` or equivalent), slow query log, database query statistics.

## CPU and algorithms

- Unnecessary complexity (quadratic work on large collections, repeated linear searches -> hash map/set, repeated sorts, expensive regex/ReDoS).
- Repeated work that can be hoisted/cached/memoized; serialization/deserialization round trips; needless conversions.
- Hot paths: lock contention, allocations in loops, expensive logging when the level is off, reflection in hot paths.
- Verification: CPU profile / flame graph, stable micro-benchmarks.

## Memory

- Excess allocation, needless copies, unbounded buffers, loading whole datasets instead of streaming/batching, wasteful data structures.
- Verification: heap/allocation profile; compare before/after.

## I/O and network

- Serial external calls that could be parallel; no batching; no caching for stable data; no compression; oversized payloads; no timeouts/retry-with-backoff/circuit breakers.
- Caching: clear keys and TTLs, correct invalidation, stampede protection, never cache per-user data globally.

## Dedupe and unnecessary complexity

Same logic implemented in multiple places; near-identical helpers; single-implementation abstractions without reason; pass-through layers; config/flags for cases that never occur; premature generalization. Consolidate when there is one clear source of truth, and protect with tests before refactoring.

## Recommendation format

Problem -> evidence/measurement -> proposed change -> expected benefit -> risk/trade-off -> effort (S/M/L).
