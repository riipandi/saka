# Attribution and Licensing

Saka is licensed under the Apache License 2.0 (see [`LICENSE`](./LICENSE)).
Copyright held by the project's contributors.

## Third-party code adapted into Saka

- **[Backlite](https://github.com/mikestefanello/backlite)** (MIT,
  © Mike Stefanello) — the task queue in `framework/queue` is a port of
  Backlite's core, adapted to this architecture (PostgreSQL, in-process
  workers). Its license notice is preserved here in accordance with the
  MIT License.

## Projects Saka learned from (no code copied)

- **[Pocket ID](https://github.com/pocket-id/pocket-id)** (AGPL-3.0) —
  the reference implementation the identity surface was ported against.
  Saka reimplements the behaviors on its own architecture; the
  deliberate departures are recorded in the project's development notes.
- **[Clerk](https://clerk.com)** — the sign-in flow semantics
  (sign-in / linking / creation as one flow with verification gates) were
  modeled after Clerk's documentation and product behavior; no Clerk
  code is used.
- **[River](https://github.com/riverqueue/river)** (MIT) — the
  scheduler's periodic-job shape follows River's design; the
  implementation is Saka's own, on top of robfig/cron.

## Third-party libraries

Saka depends on many open-source libraries — Go modules (chi, pgx,
goose, samber/do, koanf, go-oidc, jwx, go-webauthn, ConnectRPC and
friends) and npm packages (Vite+, React, TanStack, React Email, and
friends). Their licenses travel with their distributions; run
`task --list` / `go mod tidy` / `pnpm why <package>` to inspect the
dependency graph.
