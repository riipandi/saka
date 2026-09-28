# Tango Deviations from Pocket ID Upstream

This file records every place tango intentionally departs from Pocket ID (upstream,
`https://pocket-id.org/swagger.yaml` at the pinned release) — functional, structural, and scope
deviations. Read it before porting an upstream handler, and record a new deviation here in the
same change that introduces it. `.llms/endpoint-reference.md` marks rows **excluded** on the
strength of an entry here.

Status vocabulary: **deviation** (different behavior, deliberate), **extension** (tango-only
surface), **excluded** (never parity work), **hold** (deliberately undecided, do not port yet).

## Structural deviations (every endpoint)

| Deviation | Upstream | Tango |
|---|---|---|
| Transport | REST under `/api`, gin | ConnectRPC under `/rpc` for first-party surfaces; REST only for protocol/infrastructure paths (`/healthz`, `/.well-known/jwks.json`, `/static/*`, profile pictures). Original REST paths are kept in the endpoint reference for traceability only. |
| Envelope | per-endpoint JSON bodies | `pkg/responder` envelope: `status` + `message` + payload; ConnectRPC errors carry the code. |
| Pagination | `pagination[page]` / `pagination[limit]` query params | Flat optional `page` / `limit` request fields; the answer carries `metadata` (shared `tango.common.v1.ListMetadata`). |
| Sorting | `sort[column]` / `sort[direction]` query params | Flat optional `sort_by` (whitelisted, validated by protovalidate `in:`) + `sort_order` (`asc` / `desc`, validated the same way) request fields. |
| Field naming | camelCase JSON | snake_case wire names (the shared ConnectRPC codec). |
| Identifiers | row UUIDs on the wire | TypeID wire form (`user_…`, `ugrp_…`); converted at the boundary with the feature's `UUIDFromWire`/`FormatID`. |
| IDs at creation | some DTOs accept a client-supplied `id` (e.g. `UserCreateDto`) | Identifiers are always server-generated (`uuidv7()`); no request names a new row's id. |
| Client facts | `ipAddress`, `userAgent`, `device` captured ad hoc | `internal/audit` captures `ClientInfo` from the request context; a feature never threads them. |
| Audit location | `city` / `country` on `AuditLogDto` (GeoIP-resolved) | Columns exist (`public.audit_logs.country/city`) but no resolver is wired — an unwired resolver records them as missing, never guesses. Adding a GeoIP resolver is a deliberate future change, not a port. |

## Functional deviations (per endpoint)

### API keys — soft revocation (**deviation**)

Upstream `DELETE /api/api-keys/{id}` hard-deletes the row (`GORM Delete` in
`backend/internal/apikey/service.go`), so a revoked key vanishes from the list. Tango's
`RevokeAPIKey` stamps `revoked_at` and the row stays listed with the revocation visible, the
same way a session's revocation is a stamp. Idempotent: revoking a revoked key succeeds.
Observable difference: upstream's list no longer contains the key; tango's shows it revoked.
Functional effect is the same — a revoked key authenticates nothing.

### One-time access — device-token transport (**deviation**)

Upstream answers `POST /api/one-time-access-email` with `204 No Content` and delivers the device
token as a cookie (`cookie.AddDeviceTokenCookie`). Tango answers the RPC with the device token in
the response body; the exchange procedure demands it back. Same pairing semantics (the token is
bound to the requesting device), different carriage — the ConnectRPC surface has no cookie
channel to mimic.

### Account disable vs ban (**extension over upstream**)

Upstream carries a `disabled` boolean on the user DTO and nothing else. Tango keeps `disabled`
(same semantics: a disabled account cannot sign in) and adds a timed ban — `BanUser` /
`UnbanUser` with a reason and an optional expiry — with the ban refusal surfaced at sign-in and
the ban/unban notices mailed through the notification gates. The ban is tango-only surface
(`[Tango]` in the API collection).

### Signup tokens carry user groups (**parity**, upstream shape)

`CreateSignupToken` accepts `user_group_ids`; every account signed up under the token joins those
groups (`public.signup_tokens_user_groups`, cascade on token delete). A token naming no group
creates groupless accounts, the way upstream's empty list does. A group that does not exist is
refused at issue time, not halfway through a sign-up.

### Tango-only surfaces (**extension**)

Password authentication and the session lifecycle (`SignOut*`, `Refresh`, impersonation), the TOTP
second factor and its pending-bridge sign-in, password recovery, email change, key-set publication
details, and the audit filter `ListForUser` have no upstream counterpart — Pocket ID is
passkey-only. These are `[Tango]` entries in the Yaak collection and never claim upstream parity.

## Scope decisions

### Pocket ID external API — excluded (**excluded**)

Upstream's machine-facing API group (`/api/apis`, api clients, CIMD access, per-client grants —
the surfaces an external product uses to call Pocket ID's API under its own OIDC client) is **not
implemented in tango and will not be**. Tango does not need an external API, and the project's
purpose differs: tango's API keys exist for a holder's own scripting against tango's
administration surface, not to run a programmable API product for third parties. This is a
project-goal decision, not a missing feature — treat any request for it as a design discussion,
never as parity work.

### Setup initial admin — hold (**hold**)

Upstream `POST /api/signup/setup` (bootstrap of the first administrator) is deliberately not
ported yet. The decision is pending; until it lands, the first administrator is created by
seeding. Do not implement or document it as a working surface while it is on hold.

### Also excluded (per design, see `.llms/architecture.md`)

LDAP and SCIM sync (no directory to sync), `audit-logs/filters/client-names`
(tango never writes `client_name`), `GET /api/storage/sqlite-warning` (tango is
Postgres-only), and the SQLite storage engine behind it.

### OIDC provider — no longer excluded (**superseded 2026-09-28**)

The earlier scope decision — "OIDC provider/federation surfaces excluded,
single-tenant product" — is **superseded**: the user has decided tango becomes
a full OIDC provider, built in four sub-phases (client management, protocol
core, consent + introspect + PAR, device flow + device login). The transport
split the plan settled holds: management is ConnectRPC
(`tango.federation.v1`), the protocol endpoints are REST. Sub-phase a (client
management) is implemented; the protocol phases follow. The engine decision
for the protocol core is recorded in `.llms/architecture.md` under "Library
decision records".

Deviations the client-management surface carries, beside the structural ones:

- **Client id is operator-chosen** — the upstream create accepts a client id
  and tango does too, against the "identifiers are always server-generated"
  structural rule: the `client_id` is the credential a foreign client
  presents, not a row key on tango's own wire. Absent, one is generated.
- **Secrets are hashed, not encrypted** — the stored `credentials` document
  keeps a SHA-256 hash and a 4-character prefix per secret; the raw value
  exists in exactly one response. Upstream stores the same pair.
- **Logo kinds are PNG/JPEG/WebP, not SVG** — the upstream accepts SVG;
  tango refuses it because a publicly served SVG is a script host. The kind
  is sniffed from the magic bytes, so a renamed archive lands nowhere.
- **One logo, not two** — the upstream carries a light and a dark logo;
  tango's schema reserves one `logo_path`, so `hasDarkLogo` /
  `darkLogoUrl` have no counterpart yet.
