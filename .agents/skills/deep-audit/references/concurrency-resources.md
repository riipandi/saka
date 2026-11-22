# Concurrency and Resource Leaks

## Race conditions and concurrency

Look for state read/written from more than one thread/task/request/worker without clear synchronization.

- Shared mutable state: globals/singletons, in-memory caches, non-thread-safe collections, counters.
- Check-then-act (TOCTOU): "check exists -> create", "check balance -> deduct", permission check then action. Fixes: unique constraints, row-level locks (e.g. `SELECT ... FOR UPDATE`), optimistic locking (version column), atomic operations.
- DB transactions: isolation level vs possible anomalies (lost update, write skew, phantom reads), overly long transactions, external I/O inside transactions, inconsistent lock ordering (deadlocks).
- Distributed operations: two instances processing the same job, duplicate schedulers, distributed locks without TTL/fencing, out-of-order event processing.
- Idempotency keys and deduplication for consumers/webhooks.
- Async code: un-awaited promises/futures, fire-and-forget without error handling, callback ordering.
- Unsafe publication: objects exposed before full initialization, incorrect double-checked locking.
- Deadlock/livelock/starvation: nested locks, queues without consumers, missing backpressure.

Verification: the stack's race detector or sanitizer (e.g. a thread sanitizer), parallel stress tests with high iteration counts, tests that force interleavings (barriers/latches), database-level concurrency tests with two connections.

## Memory and resource leaks

- Resources opened but not closed on every path (including error paths): files, sockets, DB/HTTP connections, response bodies, cursors, transactions, temp files.
- Threads/tasks/coroutines with no termination condition (no cancellation), channels/queues never closed.
- Timers, listeners, subscribers, observers never unregistered; closures retaining large objects.
- Caches/maps/lists that grow unbounded (no TTL, size cap, or eviction); high-cardinality metric labels.
- Buffering: reading whole files/responses into memory instead of streaming; slices/substrings pinning large buffers.
- Pools: unbounded size, objects not returned, connections leaked on panic/exception.
- Reference cycles in runtimes without reliable cycle collection; retain cycles through closures.
- Graceful shutdown: in-flight requests/jobs drained, resources released, no lost work.
- Timeouts: every network/DB/lock call has a timeout and cancellation.

Verification: measure heap and counts of threads/tasks/file descriptors/connections before and after N iterations of load; use the stack's heap profiler; check connection counts on the database; run a short soak test.
