# Debug and operator utilities

The tools that make developing and operating Saka inspectable. Most of
these exist only where they make sense: some live in the **debug build**
(`task build:debug` or the dev server), some in the release binary, some
are repository scripts.

## The debug build's devtools

A debug build serves a bundle of instruments a release build refuses
(the paths answer 404 — claimed, but dead):

| Path | What it gives |
| --- | --- |
| `/debug/do` | The dependency container's own web UI: every wired service, its dependencies, its health |
| `/debug/encode-id`, `/debug/decode-id` | Translate between the wire's prefixed ids (`bkt_…`, `file_…`, `que_…`) and the UUIDs the database holds |
| `/debug/passkey/*` | Simulation pages that drive the WebAuthn ceremonies with a virtual authenticator — how the passkey flows are exercised without real hardware |

## The release binary's operator commands

| Command | What it does |
| --- | --- |
| `saka initialize` | Bootstrap a fresh database: the system seed and the first administrator (a generated password prints exactly once) |
| `saka admin:reset-password EMAIL` | Replace an administrator's credential |
| `saka migrate:up / migrate:down / migrate:status` | Apply, roll back, inspect migrations |
| `saka migrate:seed` | The development fixtures (scenario accounts, groups, keys, notifications, the conformance-suite clients) |
| `saka migrate:validate` | Check the migration set's integrity |
| `saka jwks:generate` | Provision a signing key pair (the rotation path) |
| `saka health` | The readiness checks from the CLI |

## Smoke commands (debug build)

Three commands prove the infrastructure wiring end to end without the
application layer: `loggersmoke` (every log transport answers), 
`mailersmoke` (an email leaves through the real sender), `otelsmoke`
(a trace arrives at the collector). If one of these fails, the problem
is below the features.

## The verification harnesses (repository)

| Tool | What it drives |
| --- | --- |
| `scripts/oidc-e2e.sh` | The OIDC surface, wire level: discovery, code flow, refresh, userinfo, introspection, revocation, logout — 43 checks |
| `scripts/oidc-race-probe.sh` | Concurrent replays of single-use operations: exactly one winner |
| `tools/e2e-passkey` | The passkey ladder: enroll, passwordless sign-in, step-up, with a virtual authenticator |
| `tools/e2e-oauthsso` | The OAuth SSO ladder: every linking, gating, and MFA-bridge branch against a mock provider |
| `packages/e2e-tests/conformance/` | The OpenID Foundation conformance suite driver — the four certification profiles |
| `scripts/loadtest/` | The k6 load test: smoke/load/stress over the crucial endpoints |
| `api/specs/` | The Yaak collection — 212 requests covering every surface, importable into the Yaak client |

## The task runner

`task --list` mirrors the CLI and the checks: `task lint`, `task test`,
`task config:validate`, the certificate tasks, the compose stacks, the
conformance driver. The [contributing guide](contributing.md) carries
the everyday set.
