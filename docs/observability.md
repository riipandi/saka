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

## The operations console

Two admin RPC surfaces expose the machinery's state to an operations
client (see [API endpoints](api-endpoint.md#queue--scheduler-administration)):

- **Queue** — per-queue live counts, pending tasks with decoded
  payloads, the dead-task archive, and the levers: cancel, replay, flush.
- **Scheduler** — the cron jobs with their specs and last-fired times,
  and a run-now that enqueues without touching the schedule.

## What the audit log is (and is not)

The audit log is a **product feature** — every security-relevant act
recorded for the account holder and the administrator (see
[API endpoints](api-endpoint.md#audit-logs)). It is read-only by
construction, its vocabulary is a closed set in code, and its retention
is a deployment setting applied by a scheduled job. It is not a log
aggregator; the operational logs above are the machine's, the audit log
is the users'.
