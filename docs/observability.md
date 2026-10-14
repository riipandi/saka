# Observability

Saka ships with the three observability signals wired — logs, traces,
and metrics — plus a health model that tells you whether the deployment
is alive, ready, or lying about either.

## Health: alive vs ready

| Check | Answers | Touches dependencies? |
| --- | --- | --- |
| `/healthz` | Liveness — the process is up | No |
| `/api/healthz` (and the RPC equivalent) | Readiness — the dependencies answer | Yes |

A container orchestrator liveness-probes the first and readiness-probes
the second; a load balancer can use readiness to pull the deployment
while a database recovers.

## Logs

Structured logs go to the transports the configuration names — console,
file, and/or OTLP — with request logs carrying the identity that matters
for operations: method, path, status, duration, request id. Request ids
travel with every request and can be derived from an incoming header, so
a caller's correlation id becomes the server's.

## Traces and metrics

OpenTelemetry tracing and metrics are per-signal switches in the
configuration: traces on, metrics on, one collector endpoint serving
both (and the logs, when their transport is OTLP). Metrics expose a
Prometheus endpoint at the configured path; a push interval keeps
metrics flowing while a pull-based collector is down. Tracing is off by
default — turn it on when you want to follow a request across the
queues and outbound calls.

The `otelsmoke` command (debug build) proves the pipe end to end: one
synthetic trace, sent for real.

## Browser tracing (optional)

The web client can join the same trace picture. It is off until the
configuration names a browser-facing collector:

```json
{ "otel": { "browser": { "endpoint": "https://collector.example.com:4318", "ratio": 0.25 } } }
```

- An empty `endpoint` (the default) keeps frontend tracing off — the
  browser initializes no tracer and sends nothing.
- `ratio` is the fraction of traces sampled; `0` samples everything,
  because an operator who named a collector wanted traces.
- The address must be reachable by the browser (not loopback-only) and
  the collector's OTLP/HTTP receiver must answer the CORS preflight for
  the origin the web client is served from — the bundled dev collector
  config in `container/otel-collector.yaml` shows the shape.

When enabled, the browser exports one service (`saka-web`) alongside
the server process: one span per API request it sends, a span per
navigation, and the sign-in/refresh operations with the server's spans
as their children. Query strings never leave the page — the reset
token, one-time-access code, and OAuth flow tokens travel in search
params and are stripped before export.

## The operations console

Two admin RPC surfaces expose the machinery's state to an operations
client (see [API endpoints](api-endpoint.md#queue--scheduler-administration)):

- **Queue** — per-queue live counts, pending tasks with decoded
  payloads, the dead-task archive, and the levers: cancel, replay, flush.
- **Scheduler** — the cron jobs with their specs and last-fired times,
  and a run-now that enqueues without touching the schedule.

## Database health: the maintenance queries

The queries below read the live server, not the application. Run them
from any SQL client holding an operator role (`psql` with the
deployment's DSN, or the operations client the deployment uses). None of
them change server state.

### Slow queries (`pg_stat_statements`)

The production image does not preload `pg_stat_statements` — enabling it
is a deployment-side decision (it costs a small, constant overhead per
query):

1. Add `pg_stat_statements` to `shared_preload_libraries` in the
   container's postgres configuration (a restart is required — the
   setting is boot-only).
2. Run `CREATE EXTENSION pg_stat_statements;` once per database you want
   to inspect.

The test and development containers already boot this way (see
`pkg/testutils/postgres.go`), so a reproducible query seen in a test can
be compared against production numbers. The slow-query list:

```sql
SELECT query, calls,
       round(total_exec_time / 1000.0, 1) AS total_seconds,
       round(mean_exec_time, 1)           AS mean_ms,
       rows
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 20;
```

Reset the sample window after a fix ships (it does not affect the
server):

```sql
SELECT pg_stat_statements_reset();
```

### Dead tuples and bloat

A table whose dead-tuple share grows faster than autovacuum clears it
needs attention — first a closer look at the autovacuum logs, not a
manual `VACUUM`:

```sql
SELECT relname, n_live_tup, n_dead_tup,
       round(n_dead_tup * 100.0 / greatest(n_live_tup + n_dead_tup, 1), 1) AS dead_pct,
       last_autovacuum
FROM pg_stat_user_tables
ORDER BY dead_pct DESC
LIMIT 20;
```

### Unused indexes

An index no plan has touched since the last stats reset still costs its
writes. Confirm a candidate across a full workload cycle before dropping
it (the constraint-backed ones — primary and unique keys — stay
regardless):

```sql
SELECT relname AS table, indexrelname AS index, idx_scan,
       pg_size_pretty(pg_relation_size(indexrelid)) AS size
FROM pg_stat_user_indexes
WHERE idx_scan = 0
ORDER BY pg_relation_size(indexrelid) DESC;
```

### Planner cost settings on SSD

Deployments running on SSD storage can lower `random_page_cost` from its
spinning-disk default of `4` toward `1.1` — the planner then trusts
index scans the way an SSD's random-read cost actually behaves. Set it
per instance in the postgres configuration, not per session, and compare
`EXPLAIN (ANALYZE, BUFFERS)` of a hot query before and after.

## What the audit log is (and is not)

The audit log is a **product feature** — every security-relevant act
recorded for the account holder and the administrator (see
[API endpoints](api-endpoint.md#audit-logs)). It is read-only by
construction, its vocabulary is a closed set in code, and its retention
is a deployment setting applied by a scheduled job. It is not a log
aggregator; the operational logs above are the machine's, the audit log
is the users'.

---

Back to [Documentation Index](./index.md)
