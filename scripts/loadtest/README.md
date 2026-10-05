# Load test (k6)

Load tests for the HTTP surface. The nine crucial endpoints run as tagged
scenarios, so the end-of-run summary answers each endpoint for itself; the
shape of the run is one of three profiles.

## Prerequisites

- `k6` on PATH (<https://grafana.com/docs/k6/latest/set-up/install-k6/>).
- A running server: `task dev`, or a built release binary (`build/release/saka
  serve --env-file=.env.local`). The test never starts one itself.
- A **seeded database**: `task db:migrate && task db:seed`. The test signs in
  over the real password surface, so the account must exist and carry a
  password: the seed's `admin@example.com` / `@dmin123` (override with
  `K6_IDENTITY` / `K6_PASSWORD`). The account needs the administrator grant —
  three scenarios (audit log, queue admin) are admin-gated.
- The **storage scenario** stages its own fixture: setup() creates (or reuses)
  the `k6-loadtest` bucket and uploads one object through the tus surface, so
  the storage GET serves real bytes. When the storage engine is not live
  (`storage.driver` not `local`, engine not mounted), the scenario skips and
  counts the skips under `loadtest_storage_skipped` instead of failing.

## Running

```sh
task bench:loadtest                                   # smoke, localhost:3080
task bench:loadtest --- K6_PROFILE=load               # the working shape
K6_PROFILE=stress K6_TARGET=http://host:3080 k6 run scripts/loadtest/loadtest.mjs
```

## Profiles

| Profile | Shape | Reads |
| --- | --- | --- |
| `smoke` (default) | 1 VU, 20 s, every scenario | a correctness pass: every scenario answers success before a longer run is worth starting |
| `load` | ramping VUs, 50 across the surface weighted by importance (session reads heaviest), ~4½ min | the working shape of a busy instance |
| `stress` | arrival-rate ramps to 300 req/s aggregate, ~11 min | the ceiling: the shape finds where the surface bends, not a target to live at |

`K6_VUS` / `K6_DURATION` override the load profile's targets; `K6_TARGET`
names the server (default `http://localhost:3080`).

## Scenarios

| Tag | Endpoint | Why it is crucial |
| --- | --- | --- |
| `healthz` | `GET /healthz` | the liveness floor |
| `api-healthz` | `GET /api/healthz` | the checker's aggregate read |
| `jwks` | `GET /.well-known/jwks.json` | every client's first read before verifying a token |
| `signin` | `POST /rpc … AuthService/SignIn` | the password hasher is expensive by design — the CPU ceiling a public credential surface carries |
| `get-session` | `POST …/GetSession` | the authN read every API call makes |
| `list-sessions` | `POST …/ListSessions` | a paginated account read |
| `refresh` | `POST …/Refresh` | token rotation is a write path; the scenario is serial (one VU) because a concurrent refresh is a reuse the session revokes |
| `notification-list` | `POST …/ListNotifications` | the account's most-read page |
| `auditlog-list` | `POST …/AuditLogService/List` | the heaviest faceted query |
| `admin-queues` | `POST …/ListQueues` + `ListTasks` | the administrative reads over the queue archive |
| `storage-get` | `GET /storage/{bucket}/{key}` | file serving with the immutable cache headers |

## Reading the summary

Thresholds ride the per-request `name` tag, so each endpoint carries its own
budget in the summary: reads at p95 < 300 ms (static and health at 200 ms),
`signin` at p95 < 1.5 s — a deliberate scrypt is not a slow query — and the
failure rates under 1% (5% for `signin`, because the rate limiter's 429s
count as failures there and are expected at stress rates). A scenario that
made no requests (the storage fixture skipped) passes vacuously.

A stress run whose `signin` failure rate climbs while its p95 holds is the
rate limiter engaging, not the database bending — read the
`http_req_failed{name:…}` series beside the limiter's own metrics before
concluding anything about capacity.
