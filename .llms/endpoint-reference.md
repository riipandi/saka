# Endpoint Reference (Pocket ID upstream)

Source: <https://pocket-id.org/swagger.yaml> — grouped by spec tag. Use as the Yaak
request checklist: one request per row, named `<METHOD> <path>` for REST and
`<METHOD> /rpc/<package>.<Service>/<Method>` for ConnectRPC. Status vocabulary:
**done** (implemented with test evidence in the Evidence column), **partial**
(implemented with a noted deviation), **planned** (unimplemented — owning phase
named), **excluded** (out of scope per `.llms/tango-deviations.md` — never parity work).

Tango-only extensions (not in the upstream spec) and all structural deviations (envelope,
pagination, snake_case) are documented in `.llms/tango-deviations.md` — read it before porting
upstream handlers.

## Transport split

First-party application surfaces are ConnectRPC below `/rpc`: the SPA, the admin console, and
internal tools call the generated clients from `api/connect/*.proto`. The `Endpoint` column keeps
the original REST path for traceability; it is no longer mounted. Protocol and infrastructure
surfaces stay HTTP below `/api` (or their root path) and are marked `REST`.

Protected RPCs authenticate with `Authorization: Bearer <internal-access-token>`; the admin
application API also accepts `X-API-KEY` for machine clients. Self-service and credential-lifecycle
procedures — the account surface, `UserService` self procedures, email verification, one-time
access administration, signup-token administration, MFA, and device approval — never accept a
machine credential, so a leaked key cannot rotate its owner's password or edit its owner's profile.
Cookie presence never authorizes an RPC.

Every procedure is called with `POST`; `GET` is reserved for procedures that declare
`idempotency_level = NO_SIDE_EFFECTS`, and no procedure in `api/connect/` does, so a `GET` on any
procedure answers `405` with `Allow: POST` (`internal/transport.TestRPCRejectsGet`).

The contracts frozen so far are `tango.common.v1` (`common.proto`: the shared response metadata
block) and `tango.system.v1` (`system.proto`: `HealthService`). The transport rules — snake_case
field naming on both surfaces, and an unknown `/rpc` path answering the Connect error document —
are pinned by `internal/transport/handler_rpc_test.go`.

> **Most rows below are stale.** The matrix was written for a tree that was later reset, so it
> names routes, modules, and tests that do not exist today. Do not read a **done** row as a working
> route. `docs/api-endpoint.md` is the target surface, the code is the only record of what is
> served, and the Health rows further down are the part re-verified against the current tree.

## Authentication (tango-only)

Password authentication is a tango-only surface: upstream Pocket ID signs users in with passkeys
only. The contract lives in `api/connect/auth.proto` under `tango.auth.v1` — `AuthService` issues
the credentials, `SessionService` carries the lifecycle of the session a sign-in opened. The
session is the server's one trace of an authenticated caller: who opened it, from where, under
which refresh token, and — since the lifecycle landed — whether it has ended and who ended it.

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.auth.v1.AuthService/SignIn` | Sign in with password | done — body `{identity, password, remember}`; indistinguishable failures for unknown identity vs wrong password; disabled or banned accounts fail closed; `remember` selects the long or short session lifetime; answers the token pair + session id + user view | `modules/identity/signin` (service tests), `internal/transport.TestTheSessionLifecycleEndsInAStamp` |
| POST | `/rpc/tango.auth.v1.SessionService/SignOut` | Sign out | done — guard `Session` (machine credential + sid-less token refused); stamps `revoked_at`/`revoked_by` in a transaction, idempotent; the refresh token dies with the stamp and the access token keeps working until its own expiry — the statelessness the protocol settles; audit `sign_out` names the session | `modules/identity/session.TestSignOutStampsTheRowAndTheRefreshTokenDies`, `internal/transport.TestTheSessionLifecycleEndsInAStamp` |
| POST | `/rpc/tango.auth.v1.SessionService/GetSession` | Inspect current session | done — the session's view (provider, remember, agent, address, the instant it ends) beside the account view; an ended session answers `unauthenticated`, which is the signal a resuming client needs | `modules/identity/session.TestGetSessionAnswersTheLiveRowAndRefusesAnEndedOne`, `internal/transport.TestTheSessionLifecycleEndsInAStamp` |
| POST | `/rpc/tango.auth.v1.SessionService/ListSessions` | List own sessions | done — newest first, ended ones included, the caller's own marked; the caller's own session must be live (a sign-out ends the holder's view of the list — `unauthenticated`, "the session has ended") | `modules/identity/session.TestListSessionsAnswersTheAccountsOwnNewestFirst`, `modules/identity/session.TestAnEndedSessionCannotManageSessions` |
| POST | `/rpc/tango.auth.v1.SessionService/RevokeSession` | Revoke one own session | done — guard `Session`; the caller's own session must be live; ownership is the not-found shape, an already-ended target is the same success that records nothing; audit `session_revoked` is its own event, because ending your current session and ending one you named are different happenings | `modules/identity/session.TestRevokeSessionEndsOneOfTheAccountsAndRefusesAnOthers` |
| POST | `/rpc/tango.auth.v1.SessionService/Refresh` | Refresh token pair | done — guard `Session`; the refresh token is rotated in place (the row keeps its identifier, the secret and the window are replaced), a disabled or banned account's renewal is refused, and a session ended between the read and the write costs the new secret and nothing else; no audit record — a renewal is the session continuing, not a happening an operator audits for | `modules/identity/session.TestRefreshRotatesTheTokenAndKeepsTheSession`, `internal/transport.TestTheSessionLifecycleEndsInAStamp` |
| POST | `/rpc/tango.auth.v1.SessionService/SignOutOtherSessions` | Sign out other sessions | done — guard `Session`; every live session of the account except the caller's own is stamped in one transaction under row locks, each with its own `session_revoked` record carrying the `sign_out_others` reason; the caller's own session must be live; the response counts what the call ended; the kept row and its refresh token survive | `modules/identity/session.TestSignOutOtherSessionsSweepsEveryLiveRowButTheCallerOwn`, `internal/transport.TestTheBulkSignOutsSweepTheAccountSessions` |
| POST | `/rpc/tango.auth.v1.SessionService/SignOutAllSessions` | Sign out all sessions | done — guard `Session`; every live session of the account, the caller's own included, is stamped in one transaction under row locks, each with its own `session_revoked` record carrying the `sign_out_all` reason; the caller's own session must be live; the access token itself keeps working until its own expiry — the statelessness the protocol settles — so a client that means to discard its credential drops the token pair too | `modules/identity/session.TestSignOutAllSessionsEndsTheCallerOwnRowToo`, `internal/transport.TestTheBulkSignOutsSweepTheAccountSessions` |

Shared rules: the token pair answers two lifetimes — `access_expires_in` for the JWT and
`refresh_expires_in` for the session window the row was written with; the account's identifier
travels only in its wire form: the TypeID `user_…` (`pkg/userid`) in the token's subject, in the
responses, and in the URLs — the row's UUID never leaves the server, and a request that names an
account without the prefix is refused the same way a malformed one is; the sign-in procedure rides
the tight auth rate budget; the sign-in failure never
reveals whether the identity exists; refresh tokens are SHA-256 hashed with 256 bits of base64url
randomness (`pkg/crypto.NewRefreshTokenPair`, the one draw both the opening and the renewal use);
the account-state checks are the issuer's, so every way of opening or continuing a session refuses
the same. Audit events cover sign-in, sign-out, and named revocations; a renewal records nothing.
The RPC `ForgotPassword`/`ResetPassword` twins stay unimplemented — recovery is served by the
retained REST routes below.

## One-Time Access

The codes that sign an account in without its password, ported from upstream Pocket ID's
one-time access feature. An administrator issues a code for one account or sends it by email;
an account holder asks for the email from the sign-in page. The exchange is the procedure the
frontend reaches with the code the email linked to, and it answers the token pair a password
sign-in answers with, under a session whose provider names `one_time_access`.

| Method | Procedure / Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------------------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.auth.v1.OneTimeAccessService/CreateToken` | Create one-time access token for user (admin) | done — guard `Admin`; body `{id, ttl_seconds?}` (60..86400, unset 900); the six-character form is a code that lives fifteen minutes or less, twelve above; answers `{token, expires_at}`; only the hash is stored, so the response is the last the code exists | `modules/identity/onetimeaccess.TestCreateTokenIssuesACodeTheExchangeAccepts`, `internal/transport.TestTheOneTimeAccessLoopEndsInASession` |
| POST | `/rpc/tango.auth.v1.OneTimeAccessService/ExchangeToken` | Exchange one-time access token | done — guard `Public`, the one procedure a caller reaches without a credential; body `{token, device_token?}`; the code's spend, the session it opens, and the audit record commit in one transaction, so a rollback returns the code; a device token the email request paired with the code must come back exact, and a mismatch leaves the code spendable; a disabled or banned account is refused with the code intact; answers the token pair + user view | `modules/identity/onetimeaccess.TestExchangeRefusesADeviceTokenThatDoesNotMatch`, `modules/identity/onetimeaccess.TestExchangeRefusesADisabledOrBannedAccount`, `internal/transport.TestTheOneTimeAccessGuardIsDeclared` |
| POST | `/rpc/tango.auth.v1.OneTimeAccessService/RequestEmailAsAdmin` | Request one-time access email (admin) | done — guard `Admin`; body `{id, ttl_seconds?}`; refused with `permission_denied` while `auth.one_time_access_email_as_admin_enabled` is off (default); the code travels by email alone, never through the caller; the message rides the `one_time_access_email` queue task (3 attempts, 30s timeout, 15s backoff — tighter than the verification email's, because the code expires) | `modules/identity/onetimeaccess.TestRequestEmailAsAdminSendsWithoutExposingTheCode`, `modules/identity/onetimeaccess.TestRequestEmailRefusesADisabledPath` |
| POST | `/rpc/tango.auth.v1.OneTimeAccessService/RequestEmail` | Request one-time access email | done — guard `Public`; body `{email, redirect_path?}`; refused with `permission_denied` while `auth.one_time_access_email_as_unauthenticated_enabled` is off (default); an address no account holds answers the same success a known one does, so the response is not the enumeration; the answer carries a 16-character device token the exchange demands back, real whether the address exists or not | `modules/identity/onetimeaccess.TestRequestEmailAnswersTheSameForAnUnknownAddress`, `internal/transport.TestTheOneTimeAccessGuardIsDeclared` |

Shared rules: codes are drawn from an alphabet without ambiguous characters and stored as
SHA-256 hashes; the unique index on `(user_id, purpose)` keeps an account to one code at a time,
so a re-request is a re-issue and the table never grows past the account count; an expired code
is refused and its row stays until the account's next code replaces it — the refusal runs inside
the transaction a sweep would have to survive, and the sweep is exactly what a rollback undoes.
The email links to `<base-url>/login-code?code=<token>` (plus `&redirect=` when the ask carried
a path), and the frontend forwards the code to the exchange. Audit events: `one_time_access_email_sent`
(the address only — the code is never in the record) and `one_time_access_sign_in`.

## MFA TOTP (tango-only)

Upstream Pocket ID has no TOTP; this surface is tango-only and follows the database contract in
`.llms/porting-plan/database.md` (`user_mfa_totp`, `user_mfa_recovery_codes`,
`user_mfa_pending`).

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.identity.v1.MfaService/EnrollTotp` | Start TOTP enrollment | done — self; returns the raw secret + otpauth URI exactly once; re-enroll replaces an unconfirmed row | `modules/identity/totp.TestMfaRPCLifecycle` |
| POST | `/rpc/tango.identity.v1.MfaService/ConfirmTotp` | Confirm and enable TOTP | done — verifies one code; sets `confirmed_at`; returns recovery codes exactly once | `modules/identity/totp.TestMfaRPCLifecycle` |
| POST | `/rpc/tango.identity.v1.MfaService/GetTotpStatus` | TOTP status | done — confirmed flag + remaining recovery-code count | `modules/identity/totp.TestMfaRPCLifecycle` |
| POST | `/rpc/tango.identity.v1.MfaService/VerifyPending` | Complete a pending sign-in | done — pending-auth cookie; accepts a TOTP code or a recovery code; issues the full session | `modules/identity/totp.TestMfaRPCVerifyPending` |
| POST | `/rpc/tango.identity.v1.MfaService/RotateRecoveryCodes` | Rotate recovery codes | done — requires a valid TOTP code; returns the new codes exactly once | `modules/identity/totp.TestMfaRPCLifecycle` |
| POST| `/rpc/tango.identity.v1.MfaService/DisableTotp` | Disable TOTP | done — requires the current password; drops all MFA state | `modules/identity/totp.TestMfaRPCLifecycle` |

Fixed parameters: issuer = the configured app name, 6 digits, 30-second period, SHA-1,
±1 step bounded skew. Sign-in composition: a confirmed TOTP enrollment turns a successful
password sign-in into a pending authentication (5-minute TTL, one row per user, cookie-bound)
instead of a full session; the full session is issued only by `VerifyPending`. Pending state is
never a session flag, expires server-side, is replaced on the next sign-in, and is cleared on
sign-out. TOTP verification is constant-time with step replay protection (`last_used_step`);
recovery codes are hashed, single-use, shown exactly once, and rotated atomically. Disablement
requires the current password and clears every MFA row.

## Webhooks (tango-only)

Upstream Pocket ID has no webhooks; this surface is tango-only and follows the database contract
in `.llms/porting-plan/database.md` (`webhook_endpoints`, `webhook_deliveries`,
`webhook_delivery_attempts`).

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.webhook.v1.WebhookService/List` | List webhook endpoints | done — admin guard; `enabled` and `event` filters; secrets never present | `modules/webhook.TestRPCWebhookLifecycle` |
| POST | `/rpc/tango.webhook.v1.WebhookService/Create` | Create a webhook endpoint | done — returns the signing secret exactly once | `modules/webhook.TestRPCWebhookLifecycle` |
| POST | `/rpc/tango.webhook.v1.WebhookService/Get` | Get a webhook endpoint | done — no secret field | `modules/webhook.TestRPCWebhookLifecycle` |
| POST | `/rpc/tango.webhook.v1.WebhookService/Update` | Update a webhook endpoint | done — partial update; absent fields keep values | `modules/webhook.TestRPCWebhookLifecycle` |
| POST | `/rpc/tango.webhook.v1.WebhookService/Delete` | Delete a webhook endpoint | done — deliveries survive with `webhook_id` nulled | `modules/webhook.TestRPCWebhookLifecycle` |
| POST | `/rpc/tango.webhook.v1.WebhookService/RotateSecret` | Rotate the signing secret | done — returns the new plaintext exactly once; new deliveries sign with it | `modules/webhook.TestRPCRotateSecret` |
| POST | `/rpc/tango.webhook.v1.WebhookService/Test` | Send a test delivery | done — queues a `webhook.test` delivery | `modules/webhook.TestRPCTestDelivery` |
| POST | `/rpc/tango.webhook.v1.WebhookService/ListDeliveries` | List deliveries of one endpoint | done — newest first, paginated; latest attempt rides along | `modules/webhook.TestRPCWebhookLifecycle` |
| POST | `/rpc/tango.webhook.v1.WebhookService/ListAllDeliveries` | List all deliveries | done — `event` filter; redacted response metadata only | `modules/webhook.TestRPCWebhookLifecycle` |

Delivery contract: HMAC-SHA256 over `t=<unix>,v1=<hex>` where the digest covers the signed
timestamp concatenated with the exact canonical body bytes. Headers on every delivery:
`X-Signature` (timestamp + `v1` digest, ±5-minute verification skew), `X-Webhook-Event` (event
name), `X-Webhook-Id` (endpoint id), `Content-Type: application/json`. The canonical body is the
deterministic JSON encoding of the payload, capped at 1 MiB, stored once as immutable bytes and
reused byte-for-byte by every retry — the signature therefore stays valid across retries. Custom
registration headers cannot override the signature set. Subscriptions use event names or the
`*` wildcard; an empty list receives every event. Retries run on the queue (5 attempts, 30 s
backoff, 30 s receiver deadline); non-2xx and transport failures are recorded per attempt and
pruned after a week. Rotation affects new deliveries only and never returns the stored
ciphertext.

---

## API Keys

The machine credentials an account issues for its own scripting and integrations. A key acts as
its owner through the same guard table a session does — an administrator's key administers — but
the surface that manages the keys refuses it, the way the upstream it ports disables API-key
authentication on its own routes: a credential that cannot revoke itself must not be the one
managing credentials.

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.apikey.v1.ApiKeyService/CreateAPIKey` | Create API key | done — guard `Session`, the rule a machine credential is refused by; the raw key is `<prefix>.<secret>`, drawn from the full alphanumeric alphabet with the crypto source, and shown exactly once — the row stores the SHA-256 of the presented string, so a database leak cannot replay it; the name is unique per owner (the `(name, owner)` index), the window must lie in the future, and a duplicate answers `already_exists` | `modules/apikey.TestCreateShowsTheKeyOnceAndRefusesADuplicateName` |
| POST | `/rpc/tango.apikey.v1.ApiKeyService/ListAPIKeys` | List API keys | done — the caller's own keys, newest first, revoked ones included; revoked stays listed because the revocation is a stamp the view carries, not a deletion | `modules/apikey.TestListOwnScopesToTheOwnerAndListAllSeesEverything` |
| POST | `/rpc/tango.apikey.v1.ApiKeyService/RenewAPIKey` | Renew API key | done — guard `Session`; an unexpired key is refused with `failed_precondition` (renewal is how a key lives past its expiry, not how it escapes one); an expired one earns a new secret and a new window, the reminder stamp dies with the old window, and the new raw key is shown once | `modules/apikey.TestRenewReplacesAnExpiredKeyAndRefusesALiveOne` |
| POST | `/rpc/tango.apikey.v1.ApiKeyService/RevokeAPIKey` | Revoke API key | done — guard `Session`; soft by the `revoked_at` stamp the schema reserved, idempotent (a second revocation is the same success and records nothing), and a key another account owns answers `not_found` | `modules/apikey.TestRevokeIsSoftAndIdempotent` |
| POST | `/rpc/tango.apikey.v1.ApiKeyService/ListAllAPIKeys` | List all API keys | done — guard `Admin`; tango-only, the administrative view over every key the deployment holds (upstream has none); the answer names each key's owner | `modules/apikey.TestListOwnScopesToTheOwnerAndListAllSeesEverything`, `internal/transport.TestTheAPIKeyGuardIsDeclared` |

Shared rules: the authwall is one read — the hash of the presented header is looked up against
`revoked_at IS NULL AND expires_at > now AND NOT users.disabled`, so an unknown, expired, revoked,
or disabled-owner key answers the same refusal and the disablement of an account takes effect on
its keys' next request, not at a token mint; the `last_used_at` the lookup touches is a metric,
not a decision, and its write is best-effort. The refusal never says which half failed. Audit
events: `api_key_created`, `api_key_renewed`, `api_key_revoked`, and
`api_key_expiry_email_sent` — recorded in the transaction that caused them, naming the owner in
`user_id` and the key in `resource_type`/`resource_id`. Not ported: the static API key (a
configuration credential acting as a manufactured administrator — tango issues keys through the
surface instead) and the upstream's direct-send reminder mail (tango's reminder travels the
durable queue).

## APIs

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.admin.v1.ApiService/ListApis` | List APIs | done | `modules/admin/apiaccess.TestRPCAPILifecycle` |
| POST | `/rpc/tango.admin.v1.ApiService/CreateAPI` | Create API | done | `modules/admin/apiaccess.TestRPCAPILifecycle` |
| POST | `/rpc/tango.admin.v1.ApiService/GetAPI` | Get API by ID | done | `modules/admin/apiaccess.TestRPCAPILifecycle` |
| POST | `/rpc/tango.admin.v1.ApiService/UpdateAPI` | Update API | done | `modules/admin/apiaccess.TestRPCAPILifecycle` |
| POST | `/rpc/tango.admin.v1.ApiService/DeleteAPI` | Delete API | done | `modules/admin/apiaccess.TestRPCAPILifecycle` |
| POST | `/rpc/tango.admin.v1.ApiService/SetPermissions` | Update API permissions | done | `modules/admin/apiaccess.TestRPCAPILifecycle` |
| POST | `/rpc/tango.admin.v1.ApiService/SetCimdAccess` | Update metadata document client access | done | `modules/admin/apiaccess.TestRPCAPILifecycle` |
| POST | `/rpc/tango.admin.v1.ApiService/ListAssignableClients` | List clients that can still be granted access | done | `modules/admin/apiaccess.TestRPCGrantLifecycle` |
| POST | `/rpc/tango.admin.v1.ApiService/ListClients` | List clients with access to an API | done | `modules/admin/apiaccess.TestRPCGrantLifecycle` |
| POST | `/rpc/tango.admin.v1.ApiService/GrantClient` | Grant a client access to an API | done | `modules/admin/apiaccess.TestRPCGrantLifecycle` |
| POST | `/rpc/tango.admin.v1.ApiService/RevokeClient` | Revoke a client's access to an API | done | `modules/admin/apiaccess.TestRPCGrantLifecycle` |
| POST | `/rpc/tango.admin.v1.ApiService/ListApisForClient` | List APIs a client may access | done | `modules/admin/apiaccess.TestRPCGrantLifecycle` |
| POST | `/rpc/tango.admin.v1.ApiService/ListAssignableApisForClient` | List APIs a client can still be granted | done | `modules/admin/apiaccess.TestRPCGrantLifecycle` |

## Application Configuration

| Method | Procedure / Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------------------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.admin.v1.ApplicationConfigurationService/Get` | Public bootstrap configuration | done — anonymous view the SPA reads before sign-in | `modules/admin/appconfig.TestRPCConfigBootstrapIsAnonymous` |
| POST | `/rpc/tango.admin.v1.ApplicationConfigurationService/GetAll` | List all application configurations | done — admin | `modules/admin/appconfig.TestRPCConfigLifecycle` |
| POST | `/rpc/tango.admin.v1.ApplicationConfigurationService/Update` | Update application configurations | done — partial update | `modules/admin/appconfig.TestRPCConfigLifecycle` |
| POST | `/rpc/tango.admin.v1.ApplicationConfigurationService/TestEmail` | Send test email | done — admin; defaults to the signed-in administrator | `modules/admin/appconfig.TestRPCConfigLifecycle` |
| GET | `/api/application-configuration` | List public application configurations | REST — unauthenticated bootstrap read | `modules/admin/appconfig.TestConfigCRUD` |
| POST | `/api/application-configuration/sync-ldap` | Synchronize LDAP | excluded | — |

## Application Images

| Method | Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------- | -------------------- | ------ | -------- |
| DELETE, GET, PUT | `/api/application-images/*` | Bundled application images | excluded — served from `public/images` through `/static/*` | `internal/transport.TestStaticAssetsHandler` |
| GET | `/api/storage/sqlite-warning` | SQLite storage warning | excluded — Postgres is the only supported database | — |

## Audit Logs

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.auditlog.v1.AuditLogService/List` | List audit logs | done — the caller's own records; guard `Authenticated`, so a delegated (impersonated) caller is refused | `modules/auditlog` (service tests), `internal/transport.TestTheAuditListAnswersTheCallersOwnRecordsOnly` |
| POST | `/rpc/tango.auditlog.v1.AuditLogService/ListAll` | List all audit logs | done — guard `Admin`; filters `event`, `user_id`, `search` (username/email) | `modules/auditlog` (service tests), `internal/transport.TestTheAdministrativeAuditProceduresAnswerAnAdministrator` |
| POST | `/rpc/tango.auditlog.v1.AuditLogService/ListForUser` | (tango-only) list one account's records | done — guard `Admin`; the administrative view of a single account | `modules/auditlog` (service tests) |
| POST | `/rpc/tango.auditlog.v1.AuditLogService/FilterOptions` | List filter facets | done — guard `Admin`; facets are `events` (distinct events in the table) and `users` (accounts that appear in it). Upstream's `client-names` facet is **not** ported: tango has no OIDC client, so `payload->>'client_name'` is never written | `modules/auditlog` (service tests), `internal/transport.TestTheAdministrativeAuditProceduresAnswerAnAdministrator` |

The writer is `internal/audit` (shared infrastructure, injected into the features); the reader is `modules/auditlog`. Events written today: `sign_in`, `account_created` (both sign-up and the administrator's CreateUser), `account_updated`, `account_deleted`, `email_verification_sent`, `email_verified`, `profile_picture_updated`, `profile_picture_reset`. Sign-out has no event: `modules/identity/session` is a scaffold, so nothing can sign out. Retention: `app.audit_retention_days` (default 90) applied by the `audit_cleanup` recurring job.

## Custom Claims

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.identity.v1.CustomClaimService/Suggest` | Get custom claim suggestions | done — keys ordered by usage count | `modules/admin/customclaim.TestRPCClaimLifecycle` |
| POST | `/rpc/tango.identity.v1.CustomClaimService/ListUserClaims` | List a user's custom claims | done | `modules/admin/customclaim.TestRPCClaimLifecycle` |
| POST | `/rpc/tango.identity.v1.CustomClaimService/CreateUserClaim` | Create a user custom claim | done | `modules/admin/customclaim.TestRPCClaimLifecycle` |
| POST | `/rpc/tango.identity.v1.CustomClaimService/UpdateUserClaim` | Update a user custom claim | done | `modules/admin/customclaim.TestRPCClaimLifecycle` |
| POST | `/rpc/tango.identity.v1.CustomClaimService/DeleteUserClaim` | Delete a user custom claim | done | `modules/admin/customclaim.TestRPCClaimLifecycle` |
| POST | `/rpc/tango.identity.v1.CustomClaimService/ListGroupClaims` | List a user group's custom claims | done | `modules/admin/customclaim.TestRPCClaimLifecycle` |
| POST | `/rpc/tango.identity.v1.CustomClaimService/CreateGroupClaim` | Create a group custom claim | done | `modules/admin/customclaim.TestRPCClaimLifecycle` |
| POST | `/rpc/tango.identity.v1.CustomClaimService/UpdateGroupClaim` | Update a group custom claim | done | `modules/admin/customclaim.TestRPCClaimLifecycle` |
| POST | `/rpc/tango.identity.v1.CustomClaimService/DeleteGroupClaim` | Delete a group custom claim | done | `modules/admin/customclaim.TestRPCClaimLifecycle` |

## Device Login

The device side is an integration flow and stays HTTP; the approval UI is first-party and moved to
ConnectRPC.

| Method | Procedure / Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------------------- | -------------------- | ------ | -------- |
| POST | `/api/device-login/requests` | Create device login request | REST — pairing cookie rides the response | `modules/identity/devicelogin.TestCreateApproveExchange` |
| POST | `/api/device-login/requests/{id}/exchange` | Exchange device login request | REST — long-poll; the device holds the request id | `modules/identity/devicelogin.TestCreateApproveExchange` |
| POST | `/rpc/tango.identity.v1.DeviceApprovalService/GetPendingRequest` | Inspect device login request | done — carries the user code | `modules/identity/devicelogin.TestRPCApprovalFlow`, `modules/identity/devicelogin.TestRPCApprovalValidation` |
| POST | `/rpc/tango.identity.v1.DeviceApprovalService/DecideRequest` | Decide device login request | done — carries the user code + approve flag | `modules/identity/devicelogin.TestRPCApprovalFlow` |

## Health

| Method | Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------- | -------------------- | ------ | -------- |
| GET | `/healthz` | Responds to healthchecks | REST — liveness, dependencies untouched | `internal/transport.TestAPIHealthzReportsTheChecker` |
| GET | `/api/healthz` | Readiness document | REST — per-dependency results | `internal/transport.TestAPIHealthzReportsTheChecker` |
| POST | `/rpc/tango.system.v1.HealthService/Check` | Readiness over ConnectRPC | done — the same checker and the same result as `/api/healthz`; fails with `unavailable` naming the checks that are down | `internal/transport.TestRPCCheckAnswersTheReadinessDocument`, `internal/transport.TestRPCUnhealthyAnswersUnavailable` |

## OIDC

Client administration and consents are first-party and moved to ConnectRPC. The public logo read,
the interaction pages, and every protocol endpoint stay HTTP.

| Method | Procedure / Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------------------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.federation.v1.OidcClientService/ListClients` | List OIDC clients | done | `modules/federation/oidc.TestRPCClientLifecycle` |
| POST | `/rpc/tango.federation.v1.OidcClientService/CreateClient` | Create OIDC client | done — show-once secret | `modules/federation/oidc.TestRPCClientLifecycle` |
| POST | `/rpc/tango.federation.v1.OidcClientService/GetClient` | Get OIDC client | done | `modules/federation/oidc.TestRPCClientLifecycle` |
| POST | `/rpc/tango.federation.v1.OidcClientService/UpdateClient` | Update OIDC client | done | `modules/federation/oidc.TestRPCClientLifecycle` |
| POST | `/rpc/tango.federation.v1.OidcClientService/DeleteClient` | Delete OIDC client | done | `modules/federation/oidc.TestRPCClientLifecycle` |
| POST | `/rpc/tango.federation.v1.OidcClientService/UpdateAllowedUserGroups` | Update allowed user groups | done | `modules/federation/oidc.TestRPCClientLifecycle` |
| POST | `/rpc/tango.federation.v1.OidcClientService/GetClientMeta` | Get client metadata | done | `modules/federation/oidc.TestRPCClientLifecycle` |
| POST | `/rpc/tango.federation.v1.OidcClientService/PreviewClient` | Preview OIDC client data for user | done — claim maps, no real JWTs; no focused RPC test yet | — |
| POST | `/rpc/tango.federation.v1.OidcClientService/RefreshClient` | Refresh client metadata document | done — CIMD-lite | `modules/federation/oidc.TestRPCIMDClientLifecycle` |
| POST | `/rpc/tango.federation.v1.OidcClientService/UploadLogo` | Update client logo | done — `bytes` payload, content type sniffed; no focused RPC test yet | — |
| POST | `/rpc/tango.federation.v1.OidcClientService/DeleteLogo` | Delete client logo | done; no focused RPC test yet | — |
| POST | `/rpc/tango.federation.v1.OidcClientService/ListSecrets` | List client secrets | done — multi-secret; values never returned | `modules/federation/oidc.TestClientSecretLifecycle` |
| POST | `/rpc/tango.federation.v1.OidcClientService/CreateSecret` | Create client secret | done — show-once | `modules/federation/oidc.TestClientSecretLifecycle` |
| POST | `/rpc/tango.federation.v1.OidcClientService/DeleteSecret` | Delete client secret | done | `modules/federation/oidc.TestClientSecretLifecycle` |
| POST | `/rpc/tango.federation.v1.OidcClientService/GetScimProvider` | Get SCIM service provider for a client | done; no focused RPC test yet | `modules/federation/scimsync.TestProviderGetByClient` (store-level) |
| POST | `/rpc/tango.federation.v1.OidcConsentService/ListMyAuthorizedClients` | List authorized clients for current user | done — revocation cascades to active tokens | `modules/federation/oidc.TestRPCConsentScopes` |
| POST | `/rpc/tango.federation.v1.OidcConsentService/RevokeMyAuthorizedClient` | Revoke authorization for an OIDC client | done | `modules/federation/oidc.TestRPCConsentScopes` |
| POST | `/rpc/tango.federation.v1.OidcConsentService/ListMyClients` | List accessible OIDC clients for current user | done | `modules/federation/oidc.TestRPCConsentScopes` |
| POST | `/rpc/tango.federation.v1.OidcConsentService/ListUserAuthorizedClients` | List authorized clients for a user | done — admin | `modules/federation/oidc.TestRPCConsentScopes` |
| POST | `/rpc/tango.federation.v1.OidcConsentService/ListAllAuthorizedClients` | List every authorized client | done — admin-wide | `modules/federation/oidc.TestRPCConsentScopes` |
| GET | `/api/oidc/clients/{id}/logo` | Get client logo | REST — bare image for the sign-in page | `internal/registry.TestRetainedRESTInventory` |
| GET | `/api/oidc/interaction/{id}` | Read the authorization interaction | REST — browser protocol flow | `modules/federation/oidc.TestAuthorizeRedirectsAnonymousToInteraction` |
| POST | `/api/oidc/interaction/{id}/approve` | Approve the authorization interaction | REST — browser session and redirect behavior | `modules/federation/oidc.TestEndToEndAuthorizeTokenUserinfo` |
| GET, POST | `/authorize` | Authorization endpoint | REST — redirect and OAuth error contract | `modules/federation/oidc.TestEndToEndAuthorizeTokenUserinfo` |
| POST | `/api/oidc/token` | Token endpoint | REST — form encoding, client authentication, RFC errors | `modules/federation/oidc.TestRefreshRotationAndReuseRevocation` |
| POST | `/api/oidc/introspect` | Introspect OIDC tokens | REST — client-scoped RFC 7662 | `modules/federation/oidc.TestIntrospection` |
| POST | `/api/oidc/par` | Push authorization request | REST — RFC 9126; one-time request_uri | `modules/federation/oidc.TestPARPushAndOneTimeAuthorizeResume` |
| POST | `/api/oidc/device/authorize` | Device authorization grant | REST — RFC 8628; hashed codes | `modules/federation/oidc.TestDeviceFlowIssuesTokensAfterApproval` |
| GET | `/api/oidc/device/info` | Device code info for the consent page | REST | `modules/federation/oidc.TestDeviceFlowIssuesTokensAfterApproval` |
| POST | `/api/oidc/device/verify` | Approve or deny a device code | REST — browser session; single approval | `modules/federation/oidc.TestDeviceFlowDenialDeniesThePoll` |
| GET | `/api/oidc/userinfo` | Get user information | REST — bearer token, RFC-style errors | `modules/federation/oidc.TestEndToEndAuthorizeTokenUserinfo` |
| GET, POST | `/api/oidc/end-session` | RP-initiated logout | REST — redirect behavior | `modules/federation/oidc.TestEndSessionRevokesFamilyAndRedirects` |

The protocol endpoints are verified by the same suite: `/authorize` →
`modules/federation/oidc.TestEndToEndAuthorizeTokenUserinfo` and friends. It
accepts an RFC 9126 `request_uri` in place of inline parameters (one-time; a replayed push is
rejected); `/api/oidc/end-session` → `modules/federation/oidc.TestEndSessionRevokesFamilyAndRedirects`:
the `id_token_hint` must verify (issuer, audience, subject, jti), the `client_id` must match the
hint's audience, and the user must have granted the client — every failure redirects to the
instance logout page without explaining why. Ending the session deactivates the grant's whole
token family (the ID token carries the access token's `jti`), replays are idempotent, and an
unregistered `post_logout_redirect_uri` is never followed;
`/.well-known/*` and JWKS → `modules/federation/discovery` and `modules/federation/jwks`. The
discovery document advertises the device, PAR, and introspection endpoints plus upstream's
metadata fields (`response_modes_supported`, `prompt_values_supported`, `grant_types_supported`
with the device grant, `authorization_response_iss_parameter_supported`,
`client_id_metadata_document_supported: false`, `require_pushed_authorization_requests: false`).
Device-flow codes are stored hashed; the poll answers `authorization_pending`, `slow_down`,
`expired_token`, and `access_denied` per RFC 8628 §3.5.

## SCIM

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.federation.v1.ScimProviderService/Upsert` | Create SCIM service provider | done | `modules/federation/scimsync.TestRPCProviderLifecycle` |
| POST | `/rpc/tango.federation.v1.ScimProviderService/Update` | Update SCIM service provider | done | `modules/federation/scimsync.TestRPCProviderLifecycle` |
| POST | `/rpc/tango.federation.v1.ScimProviderService/Delete` | Delete SCIM service provider | done | `modules/federation/scimsync.TestRPCProviderLifecycle` |
| POST | `/rpc/tango.federation.v1.ScimProviderService/Sync` | Sync SCIM service provider | done — queues outbound sync | `modules/federation/scimsync.TestRPCProviderLifecycle` |

## User Groups

The groups accounts belong to. A group carries no permission of its own; the members it gathers
are addressed together, and a later feature may hang claims on a group or gate a client on it.
Every procedure is administrative — upstream guards the surface with its admin middleware — and
the member count every answer carries is what the query computes, never a column.

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.identity.v1.UserGroupService/ListUserGroups` | List user groups | done — the group's identifier travels in its wire form, the TypeID `ugrp_…` (`modules/identity/usergroup`), and a request that names a group without the prefix is refused; page/limit/search over the name and the display name; sorted by `name`, `display_name`, `user_count`, or `created_at`, ascending by default, the name columns case-insensitively; the LEFT join makes an empty group a row with a zero count, not an absence | `modules/identity/usergroup.TestListGroupsSearchesPaginatesAndSorts`, `internal/transport.TestTheUserGroupLoopEndsInAMemberList` |
| POST | `/rpc/tango.identity.v1.UserGroupService/GetUserGroup` | Get user group by ID | done — the detail carries the members as the account wire view, ordered by username | `modules/identity/usergroup.TestCreateGroupStoresTheRowAndRefusesADuplicateName`, `internal/transport.TestTheUserGroupLoopEndsInAMemberList` |
| POST | `/rpc/tango.identity.v1.UserGroupService/CreateUserGroup` | Create user group | done — the duplicate name is the unique index's answer read from the write's failure, mapped to `already_exists`; the created row is read back inside the transaction | `modules/identity/usergroup.TestCreateGroupStoresTheRowAndRefusesADuplicateName` |
| POST | `/rpc/tango.identity.v1.UserGroupService/UpdateUserGroup` | Update user group | done — full replace of the two fields; a name another group holds is refused and the other group stays intact; an unknown identifier is `not_found` | `modules/identity/usergroup.TestUpdateGroupReplacesTheFieldsAndRefusesADuplicate` |
| POST | `/rpc/tango.identity.v1.UserGroupService/DeleteUserGroup` | Delete user group | done — the membership rows die with the group by the foreign keys' cascade, the accounts are untouched; the record of the deletion names the member count it took away, the one fact a later reader cannot reconstruct | `modules/identity/usergroup.TestDeleteGroupRemovesTheMemberships` |
| POST | `/rpc/tango.identity.v1.UserGroupService/SetUserGroupMembers` | Update users in a group | done — the replace, not a delta: an empty list empties the group; every identifier must name an account, and a member that does not exist refuses the replacement whole, so the group keeps the set it held | `modules/identity/usergroup.TestSetMembersReplacesTheWholeSet` |

Audit events: `group_created`, `group_updated`, `group_deleted`, and `group_members_updated` —
the membership change is its own event, because the log's one filter cannot see inside a payload.
Upstream records nothing for groups; tango records every administrative write, the way it does
for accounts. Not ported: the allowed-OIDC-clients update (the federation surface decides it),
the LDAP guards (tango has no LDAP), and the custom claims a group carries (the customclaim
feature owns them when it lands).

## Users

| Method | Procedure / Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------------------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.identity.v1.SignupService/Signup` | Sign up | done — requires a valid signup token; password and names required; created unverified; answers the canonical account view | `modules/identity/signup` (service tests) |
| POST | `/rpc/tango.identity.v1.SignupService/GetSetupAvailability` | Check initial admin setup availability | planned — needs the setup flow; not yet implemented | — |
| POST | `/rpc/tango.identity.v1.SignupService/SetupInitialAdmin` | Sign up initial admin user | planned — needs the setup flow; not yet implemented | — |
| POST | `/rpc/tango.identity.v1.SignupService/ListSignupTokens` | List signup tokens | done — admin Bearer; paginated | `modules/identity/signup` (service tests) |
| POST | `/rpc/tango.identity.v1.SignupService/CreateSignupToken` | Create signup token | done — admin Bearer; raw token shown once | `modules/identity/signup` (service tests) |
| POST | `/rpc/tango.identity.v1.SignupService/DeleteSignupToken` | Delete signup token | done — admin Bearer | `modules/identity/signup` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/ListUsers` | List users | done — admin Bearer; paginated; optional search over username/email/display_name | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/GetUser` | Get user by ID | done — admin Bearer | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/CreateUser` | Create user | done — admin Bearer; mandatory names; optional password (absent = no credential) | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/UpdateUser` | Update user | done — admin Bearer; full replace; mandatory names; ban fields as a unit | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/DeleteUser` | Delete user | done — admin Bearer; refuses the signed-in account | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/UpdateMe` | Update current user | planned — self-service profile; not yet implemented | — |
| PUT | `/api/users/{id}/profile-picture` | Update user profile picture | done — REST raw-body upload; self-service Bearer (guard `Self("id")` on the path param); magic-byte sniff (PNG/JPEG/WebP), max 2 MiB; stored at `avatars/<id>.<ext>` with the extension the sniffed bytes earn, so a kind change moves the key and deletes the replaced picture first; staged then synced in-request | `modules/identity/user` (service + handler tests), `internal/guard` (rule) |
| POST | `/rpc/tango.identity.v1.UserService/ResetProfilePicture` | Reset user profile picture | done — self-service Bearer (guard `Self("id")`); deletes the stored file and clears the row | `modules/identity/user` (service tests), `internal/guard` (rule), `internal/transport` (guard) |
| POST | `/rpc/tango.identity.v1.UserService/ListWebAuthnCredentials` | List user passkeys | planned — needs the webauthn feature; not yet implemented | — |
| POST | `/rpc/tango.identity.v1.UserService/UpdateWebAuthnCredential` | Rename user passkey | planned — needs the webauthn feature; not yet implemented | — |
| POST | `ImpersonateUser` (procedure name TBD) | Impersonate a user (admin) | TODO(impersonation) — **not implemented**: no procedure, nothing sets `AccessClaims.ActorID`, `public.sessions.impersonated_by` unused. The guard rule that *refuses* an impersonated caller on self-service requests is in place and tested (`internal/guard`), which is the half that had to land first | `pkg/jwtutils` (claim round-trip), `internal/guard` (refusal), `internal/transport` (guard) |
| POST | `StopImpersonating` (procedure name TBD) | Stop impersonating | TODO(impersonation) — **not implemented**; must be `Authenticated` in `guard.ProcedureRules`, not `Self`, because it has to be callable while the delegation is active | — |
| POST | `/rpc/tango.identity.v1.UserService/DeleteWebAuthnCredential` | Delete user passkey | planned — needs the webauthn feature; not yet implemented | — |
| POST | `/rpc/tango.auth.v1.OneTimeAccessService/RequestEmail` | Request one-time access email | done — public; anti-enumeration: an unknown address answers the same success and a real device token; refused with `permission_denied` while `auth.one_time_access_email_as_unauthenticated_enabled` is off | `modules/identity/onetimeaccess.TestRequestEmailAnswersTheSameForAnUnknownAddress` |
| POST | `/rpc/tango.auth.v1.OneTimeAccessService/RequestEmailAsAdmin` | Request one-time access email (admin) | done — admin; refused with `permission_denied` while `auth.one_time_access_email_as_admin_enabled` is off; the code travels by email alone | `modules/identity/onetimeaccess.TestRequestEmailAsAdminSendsWithoutExposingTheCode` |
| POST | `/rpc/tango.auth.v1.OneTimeAccessService/CreateToken` | Create one-time access token for user (admin) | done — admin; the six-character code is the short window’s form; only the hash is stored, so the response is the last the code exists | `modules/identity/onetimeaccess.TestCreateTokenIssuesACodeTheExchangeAccepts` |
| POST | `/rpc/tango.auth.v1.OneTimeAccessService/ExchangeToken` | Exchange one-time access token | done — public; the code’s spend, the session, and the audit record commit in one transaction, so a rollback returns the code; a device token the email request paired with the code must come back exact | `modules/identity/onetimeaccess.TestExchangeRefusesADeviceTokenThatDoesNotMatch`, `internal/transport.TestTheOneTimeAccessLoopEndsInASession` |
| POST | `/rpc/tango.identity.v1.EmailVerificationService/SendEmail` | Send email verification | done — self-service Bearer; refuses verified; resend cooldown on last_sent_at; token row upserted, email via the durable queue | `modules/identity/verification` (service tests, Mailpit end-to-end) |
| POST | `/rpc/tango.identity.v1.EmailVerificationService/VerifyEmail` | Verify email | done — public; token is the credential; consumed on success | `modules/identity/verification` (service tests) |
| GET | `/api/users/{id}/profile-picture.png` | Get user profile picture | done — REST; public; streams the stored bytes, an account without one answers the bundled default by redirect to `/images/default-avatar.png` | `modules/identity/user` (handler test) |

## WebAuthn

Passkey ceremonies are a browser contract and stay HTTP.

| Method | Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------- | -------------------- | ------ | -------- |
| POST | `/api/webauthn/register/begin` | Begin passkey registration | done — bare `publicKey` options plus an explicit ceremony id | `modules/identity/webauthn.TestRegisterBeginReturnsOptions` |
| POST | `/api/webauthn/register/finish` | Finish passkey registration | done | `modules/identity/webauthn.TestRegisterBeginReturnsOptions` |
| POST | `/api/webauthn/login/begin` | Begin discoverable passkey login | done — bare `publicKey` options plus an explicit ceremony id | `modules/identity/webauthn.TestLoginBeginAnonymousAndFinishValidation` |
| POST | `/api/webauthn/login/finish` | Finish discoverable passkey login | done — fail closed on unknown ceremony sessions | `modules/identity/webauthn.TestLoginBeginAnonymousAndFinishValidation` |

## Version

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.system.v1.VersionService/Current` | Get current deployed version | done — bearer required | `internal/transport.TestRPCVersionAuthBranches`, `internal/transport.TestRPCVersionCurrentWithoutInterceptor` |
| POST | `/rpc/tango.system.v1.VersionService/Latest` | Get latest available version | done — anonymous; falls back to the deployed build when the feed never answered | `internal/transport.TestRPCVersionAuthBranches`, `internal/transport.TestRPCVersionCurrentWithoutInterceptor` |

## Utilities

Debug-build only: unauthenticated, mounted outside the throttled and bearer-guarded groups, and answered
with a 404 envelope by a release build. Yaak folder `Utilities`.

| Method | Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------- | -------------------- | ------ | -------- |
| GET | `/debug/do` | samber/do web UI (scope tree, service inspection) | debug build only | `internal/transport/devtool_debug.go` |
| POST | `/debug/encode-id` | Encode Type ID | debug build only — body `{"prefix","uuid"}`, answers the TypeID form | `internal/transport/devtool_debug.go` |
| POST | `/debug/decode-id` | Decode Type ID | debug build only — body `{"id"}`, answers prefix + uuid + id | `internal/transport/devtool_debug.go` |

## Well Known

| Method | Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------- | -------------------- | ------ | -------- |
| GET | `/.well-known/jwks.json` | Get JSON Web Key Set (JWKS) | REST | `modules/federation/discovery.TestOpenIDConfigurationHandler` |
| GET | `/.well-known/oauth-authorization-server` | Get OAuth 2.0 authorization server metadata | REST | `modules/federation/discovery.TestOpenIDConfigurationHandler` |
| GET | `/.well-known/openid-configuration` | Get OpenID Connect discovery configuration | REST | `modules/federation/discovery.TestOpenIDConfigurationHandler` |
