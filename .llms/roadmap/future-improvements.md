---
status: living
updated: 2026-10-14T10:15:00+07:00
references: issue-20261014_0935, issue-20261014_0949, issue-20261014_0936 (archived), issue-20261014_0937 (archived)
---

# Roadmap — future improvements

Parked improvements, collected 2026-10-14 from the live issues so the
aspirational work stops floating as trigger-gated issue items. This
document is not a commitment and not a plan: an entry here describes what
would be built, the evidence that the tree can carry it, and the unlock —
the event that makes it worth planning. When an unlock fires, the entry
re-enters `.llms/issues/` as a fresh item or goes straight to a plan; the
source issue stamps stay with each entry for the trail.

Every entry's facts were verified against the tree on 2026-10-14 unless
stamped otherwise.

## OIDC protocol & federation

### RI-1 — RFC 8707 resource indicators on the provider
*(from issue-20261014_0935 ISS-002)*

- status: parked (unlock: the first internal MCP server lands) · updated 2026-10-14

MCP servers must validate a token's audience against their own canonical
URI; without the binding, one MCP server accepts tokens minted for
another. The pinned engine supports it — `WithResourceIndicators`
(`pkg/provider/option.go:1590`) and `WithResourceIndicatorsRequired`
(`:1604`) in `github.com/luikyv/go-oidc@v0.25.0` — and the provider build
(`modules/federation/oidc/protocol.go:127-164`) names no
resource-indicator option. MCP clients send `resource` regardless, so
enabling is backward-safe.

**Unlock:** the first internal MCP server lands. **First move:** a plan
phase on `modules/federation/oidc/protocol.go` wiring both options, plus
the discovery members the enablement implies (the decorator treatment the
back-channel members got).

### RI-2 — Internal MCP server integration
*(from issue-20261014_0935 ISS-003)*

- status: parked (unlock: the owner's MCP fleet plan) · updated 2026-10-14

Saka as the authorization server for the owner's MCP fleet: Basic OP +
Config OP already suffice; Protected Resource Metadata (RFC 9728) is the
MCP servers' side; DCR stays off (the MCP spec prefers pre-registration).
The requirements analysis lives in `.llms/openid.md` ("Internal MCP
servers").

**Unlock:** the owner's MCP fleet plan. **First move:** ride RI-1's
binding, then the PRM documents.

### RI-3 — Dynamic OP (dynamic client registration)
*(from issue-20261014_0935 ISS-004)*

- status: parked (unlock: a real external-developer ecosystem) · updated 2026-10-14

Opening DCR is a security-boundary change: a write path, a registration
policy, a conformance plan. The closed stance is
plan-20261005_1845's out-of-scope record — the mounted `/oidc/register`
refusal (`modules/federation/oidc/protocol.go:55`) is the final shape
until this entry opens. The MCP spec itself demotes it
(pre-registration first).

**Unlock:** a real external-developer ecosystem. **First move:** a plan
shaped like plan-20260929_1930's optional-phase pattern.

### RI-4 — Form Post OP response mode
*(from issue-20261014_0935 ISS-005)*

- status: parked (unlock: a client that cannot receive the query response mode) · updated 2026-10-14

Cheap to enable and zero demand today: the authorize grant still serves
`response_type=code` alone (`modules/federation/oidc/protocol.go:155`).
`response_mode=form_post` rides the engine; the conformance plan run and
the E2E paths come along when it happens.

**Unlock:** a client that cannot receive the query response mode.

### RI-5 — The RP conformance rehearsal (client side)
*(from issue-20261014_0935 ISS-006)*

- status: parked (unlock: a certification claim for the RP side) · updated 2026-10-14

The OAuth SSO surface (Basic RP + Config RP) is implemented and
ladder-tested but never rehearsed against the suite's `oidcc-client-*`
plans — a separate harness where the suite acts as the OP.

**Unlock:** a certification claim for the RP side.

### RI-6 — RP-initiated and back-channel logout on the RP side
*(from issue-20261014_0935 ISS-007)*

Saka never ends the upstream session on sign-out, and no receiver accepts
providers' logout tokens — usually not what users expect, revisited only
on demand. The offboarding seam (`modules/identity/oauthsso/offboarding.go`)
owns the local half; the upstream half is the entry's work.

**Unlock:** a product decision to propagate sign-out upstream.

## CLI & tooling

### RI-7 — the `storage:migrate` command
*(from issue-20261014_0949)*

- status: parked (unlock: an existing deployment changes `storage.driver`) · updated 2026-10-14

Moving stored objects between the local and S3 drivers — an old idea,
never started; `cmd/manage.go` carries only `admin:reset-password`. The
storage layer's local/S3 duality is the seam the command would walk.

**Unlock:** an existing deployment changing `storage.driver`.

### RI-8 — replacing `rs/cors`
*(from issue-20261014_0949)*

- status: parked (owner-deferred 2026-09-27; unlock: a CORS requirement `rs/cors` cannot express) · updated 2026-10-14

The library is still the router's CORS layer (`go.mod:42`); the owner
deferred the replacement as optional when the CORS defaults were reworked
(2026-09-27).

**Unlock:** a CORS requirement `rs/cors` cannot express (per-route
policies, header propagation into the response pipeline).

## Dependencies & toolchain (upstream-gated)

### RI-9 — dropping connect-go v1 from `go.mod`
*(from issue-20261014_0936)*

- status: parked (unlock: the next `buf` release on connect v2, or a dependency-graph cleanup) · updated 2026-10-14

`connectrpc.com/connect v1.21.0 // indirect` (`go.mod:94`) survives only
while `buf.build/buf` links v1; our code imports only `/v2` (zero v1
imports — the migration's grep pass proved it).

**Unlock:** the next `buf` release that moves to connect v2, or any
dependency-graph cleanup. **First move:** `go mod tidy` — the line drops.

### RI-10 — adopting `validate`/`otelconnect` `/v2` when they tag
*(from issue-20261014_0936)*

- status: parked (unlock: a v2 tag on either package, or the next upgrade) · updated 2026-10-14

`go.mod:10-11` pins `otelconnect v0.12.0` and `validate v0.9.0`; the v2
API rides the existing paths.

**Unlock:** `go list -m -versions connectrpc.com/validate` shows a v2
tag, or the next upgrade of either package. **First move:** the import
path changes, nothing else.

### RI-11 — restoring `vp run typecheck` in the Docker build
*(from issue-20261014_0937)*

- status: parked (unlock: a fixed vp ships, the reproduction reported upstream) · updated 2026-10-14

`vp run` crashes child processes with a native stack overflow in Linux
containers (fspy preload × Node 24; vp 1.0.0 and 1.1.0 both); the
Dockerfile runs the direct `tsc` chain (`deploy/Dockerfile:99-105`) and
`scripts/check-typecheck-parity.sh` holds the two sides together.

**Unlock:** a fixed vp ships (and the reproduction is reported upstream).
**First move:** restore `vp run typecheck` in the Dockerfile, delete the
parity guard, close the trail.
.
