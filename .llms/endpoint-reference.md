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

Protected RPCs authenticate with `Authorization: Bearer <access token>`; the administrative
surfaces also accept `X-API-KEY` for machine clients. Self-service and credential-lifecycle
procedures — `UserService` self procedures, email verification, one-time access
administration, signup-token administration, MFA, and the session lifecycle — never accept a
machine credential, so a leaked key cannot rotate its owner's password or edit its owner's profile.
Cookie presence never authorizes an RPC.

Every procedure is called with `POST`; `GET` is reserved for procedures that declare
`idempotency_level = NO_SIDE_EFFECTS`, and no procedure in `api/connect/` does, so a `GET` on any
procedure answers `405` with `Allow: POST` (`internal/transport.TestRPCRejectsGet`).

The rate limiter counts a named surface only: the public mutation procedures and the email
senders ride the credential bucket (`rate_limit.auth_limit`, 10 per window per IP), the refresh
rides the default bucket (`rate_limit.limit`), and everything else — reads, administrative
writes, and every call a bearer-guarded caller makes — is outside the limiter's books. The
classification is the guard's tables (`internal/guard/ratelimit.go`), the budgets the
configuration's; a public procedure missing from the tables fails a test rather than slipping
past uncounted.

The contracts frozen so far are `tango.common.v1` (`common.proto`: the shared response metadata
block) and `tango.system.v1` (`system.proto`: `HealthService`). The transport rules — snake_case
field naming on both surfaces, and an unknown `/rpc` path answering the Connect error document —
are pinned by `internal/transport/handler_rpc_test.go`.

> This matrix was reconciled against the tree on 2026-09-27: every row marked **done** is backed
> by code and the named tests exist; rows for features that were never built say **planned**.
> The code is still the only record of what is served — re-verify against it before trusting a
> row, and keep a row's Evidence honest in the same change that ships the endpoint.

## Authentication (tango-only)

Password authentication is a tango-only surface: upstream Pocket ID signs users in with passkeys
only. The contract lives in `api/connect/authn.proto` under `tango.authn.v1` — `AuthService` issues
the credentials, `SessionService` carries the lifecycle of the session a sign-in opened. The
session is the server's one trace of an authenticated caller: who opened it, from where, under
which refresh token, and — since the lifecycle landed — whether it has ended and who ended it.

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.authn.v1.AuthService/SignIn` | Sign in with password | done — body `{identity, password, remember}`; indistinguishable failures for unknown identity vs wrong password; disabled or banned accounts fail closed; `remember` selects the long or short session lifetime; answers the token pair + session id + user view; an account keeping a confirmed factor answers the MFA challenge instead (`mfa_required`, no tokens, `CompleteSignIn` spends the bridge), and while `mfa.required` stands an account keeping none answers the enrollment fork — `mfa_required` + `mfa_enrollment_required` with a bridge that admits the enrollment pair's optional `pending_token`, no token until a factor is confirmed | `modules/identity/signin` (service tests), `modules/identity/multifactor.TestTheRequiredGateRoutesAFactorlessSignInToEnrollment`, `internal/transport.TestTheSessionLifecycleEndsInAStamp` |
| POST | `/rpc/tango.authn.v1.SessionService/SignOut` | Sign out | done — guard `Session` (machine credential + sid-less token refused); stamps `revoked_at`/`revoked_by` in a transaction, idempotent; the refresh token dies with the stamp and the access token keeps working until its own expiry — the statelessness the protocol settles; audit `sign_out` names the session | `modules/identity/session.TestSignOutStampsTheRowAndTheRefreshTokenDies`, `internal/transport.TestTheSessionLifecycleEndsInAStamp` |
| POST | `/rpc/tango.authn.v1.SessionService/GetSession` | Inspect current session | done — the session's view (provider, remember, agent, address, the instant it ends) beside the account view; an ended session answers `unauthenticated`, which is the signal a resuming client needs | `modules/identity/session.TestGetSessionAnswersTheLiveRowAndRefusesAnEndedOne`, `internal/transport.TestTheSessionLifecycleEndsInAStamp` |
| POST | `/rpc/tango.authn.v1.SessionService/ListSessions` | List own sessions | done — newest first, ended ones included, the caller's own marked; the caller's own session must be live (a sign-out ends the holder's view of the list — `unauthenticated`, "the session has ended") | `modules/identity/session.TestListSessionsAnswersTheAccountsOwnNewestFirst`, `modules/identity/session.TestAnEndedSessionCannotManageSessions` |
| POST | `/rpc/tango.authn.v1.SessionService/RevokeSession` | Revoke one own session | done — guard `Session`; the caller's own session must be live; ownership is the not-found shape, an already-ended target is the same success that records nothing; audit `session_revoked` is its own event, because ending your current session and ending one you named are different happenings | `modules/identity/session.TestRevokeSessionEndsOneOfTheAccountsAndRefusesAnOthers` |
| POST | `/rpc/tango.authn.v1.SessionService/Refresh` | Refresh token pair | done — guard `Session`; the refresh token is rotated in place (the row keeps its identifier, the secret and the window are replaced), a disabled or banned account's renewal is refused, an idle session — last activity older than `session.inactivity_timeout` — is refused and revoked on the spot, and a session ended between the read and the write costs the new secret and nothing else; no audit record — a renewal is the session continuing, not a happening an operator audits for | `modules/identity/session.TestRefreshRotatesTheTokenAndKeepsTheSession`, `modules/identity/session.TestAnIdleSessionIsRefusedAndRotatedOut`, `internal/transport.TestTheSessionLifecycleEndsInAStamp` |
| POST | `/rpc/tango.authn.v1.SessionService/SignOutOtherSessions` | Sign out other sessions | done — guard `Session`; every live session of the account except the caller's own is stamped in one transaction under row locks, each with its own `session_revoked` record carrying the `sign_out_others` reason; the caller's own session must be live; the response counts what the call ended; the kept row and its refresh token survive | `modules/identity/session.TestSignOutOtherSessionsSweepsEveryLiveRowButTheCallerOwn`, `internal/transport.TestTheBulkSignOutsSweepTheAccountSessions` |
| POST | `/rpc/tango.authn.v1.SessionService/SignOutAllSessions` | Sign out all sessions | done — guard `Session`; every live session of the account, the caller's own included, is stamped in one transaction under row locks, each with its own `session_revoked` record carrying the `sign_out_all` reason; the caller's own session must be live; the access token itself keeps working until its own expiry — the statelessness the protocol settles — so a client that means to discard its credential drops the token pair too | `modules/identity/session.TestSignOutAllSessionsEndsTheCallerOwnRowToo`, `internal/transport.TestTheBulkSignOutsSweepTheAccountSessions` |

Shared rules: the token pair answers two lifetimes — `access_expires_in` for the JWT and
`refresh_expires_in` for the session window the row was written with; the account's identifier
travels only in its wire form: the TypeID `user_…` (`pkg/userid`) in the token's subject, in the
responses, and in the URLs — the row's UUID never leaves the server, and a request that names an
account without the prefix is refused the same way a malformed one is; the sign-in procedure rides
the tight auth rate budget; the sign-in failure never
reveals whether the identity exists; refresh tokens are SHA-256 hashed with 256 bits of base64url
randomness (`pkg/crypto.NewRefreshTokenPair`, the one draw both the opening and the renewal use);
The account-state checks are the issuer's, so every way of opening or continuing a session refuses
the same. Audit events cover sign-in, sign-out, and named revocations; a renewal records nothing.

## Password Recovery (tango-only)

Upstream Pocket ID has no passwords, so the whole flow is tango's. The contract lives in
`api/connect/authn.proto` under `PasswordRecoveryService`; the implementation is
`modules/identity/password` (`recovery_*`).

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.authn.v1.PasswordRecoveryService/ForgotPassword` | Forgot password | done — guard `Public`; anti-enumeration: an unknown address answers the same success; a mailer-less run refuses; the token is 256 bits of lowercase hex, shown once in the email and stored only as a hash | `modules/identity/password.TestForgotPasswordStaysSilentAboutTheAccountsItDoesNotKnow`, `modules/identity/password.TestForgotPasswordIssuesOneTokenPerAccount` |
| POST | `/rpc/tango.authn.v1.PasswordRecoveryService/ResetPassword` | Reset password | done — guard `Public`; the token is the credential; the swap, the session termination, and the audit record commit in one transaction, so a rollback returns the token; `terminate_sessions` selects whether the live sessions die with the credential | `modules/identity/password.TestResetPasswordSwapsTheCredentialAndEndsTheSessions`, `modules/identity/password.TestResetPasswordRefusesAnUnknownAnExpiredAndAWeakCredential` |
| POST | `/rpc/tango.authn.v1.PasswordRecoveryService/AdminResetUserPassword` | Reset a user's password (admin) | done — guard `Admin`; answers the states `ForgotPassword` hides (unknown account, banned, no address); the flow then travels by email like a self-service reset | `modules/identity/password.TestAdminResetTriggerReportsTheStatesForgotPasswordHides` |

Shared rules: the password-changed notice to the account's address rides the durable queue and is
gated by `mailer.notifications.password_changed_notice_enabled`; a reset that ends sessions ends
them in the same transaction. Impersonating administrators are refused.

## One-Time Access

The codes that sign an account in without its password, ported from upstream Pocket ID's
one-time access feature. An administrator issues a code for one account or sends it by email;
an account holder asks for the email from the sign-in page. The exchange is the procedure the
frontend reaches with the code the email linked to, and it answers the token pair a password
sign-in answers with, under a session whose provider names `one_time_access`.

| Method | Procedure / Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------------------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.authn.v1.OneTimeAccessService/CreateToken` | Create one-time access token for user (admin) | done — guard `Admin`; body `{id, ttl_seconds?}` (60..86400, unset 900); the six-character form is a code that lives fifteen minutes or less, twelve above; answers `{token, expires_at}`; only the hash is stored, so the response is the last the code exists | `modules/identity/onetimeaccess.TestCreateTokenIssuesACodeTheExchangeAccepts`, `internal/transport.TestTheOneTimeAccessLoopEndsInASession` |
| POST | `/rpc/tango.authn.v1.OneTimeAccessService/ExchangeToken` | Exchange one-time access token | done — guard `Public`, the one procedure a caller reaches without a credential; body `{token, device_token?}`; the code's spend, the session it opens, and the audit record commit in one transaction, so a rollback returns the code; a device token the email request paired with the code must come back exact, and a mismatch leaves the code spendable; a disabled or banned account is refused with the code intact; answers the token pair + user view | `modules/identity/onetimeaccess.TestExchangeRefusesADeviceTokenThatDoesNotMatch`, `modules/identity/onetimeaccess.TestExchangeRefusesADisabledOrBannedAccount`, `internal/transport.TestTheOneTimeAccessGuardIsDeclared` |
| POST | `/rpc/tango.authn.v1.OneTimeAccessService/RequestEmailAsAdmin` | Request one-time access email (admin) | done — guard `Admin`; body `{id, ttl_seconds?}`; refused with `permission_denied` while `auth.one_time_access_email_as_admin_enabled` is off (default); refused with `resource_exhausted` inside the one-minute resend cooldown the last issued code stamped; the code travels by email alone, never through the caller; the message rides the `one_time_access_email` queue task (3 attempts, 30s timeout, 15s backoff — tighter than the verification email's, because the code expires) | `modules/identity/onetimeaccess.TestRequestEmailAsAdminSendsWithoutExposingTheCode`, `modules/identity/onetimeaccess.TestRequestEmailAsAdminRefusesInsideTheCooldown`, `modules/identity/onetimeaccess.TestRequestEmailRefusesADisabledPath` |
| POST | `/rpc/tango.authn.v1.OneTimeAccessService/RequestEmail` | Request one-time access email | done — guard `Public`; body `{email}`; refused with `permission_denied` while `auth.one_time_access_email_as_unauthenticated_enabled` is off (default); an address no account holds answers the same success a known one does, so the response is not the enumeration; a request inside the one-minute resend cooldown holds the send and answers that same success — the earlier code stays standing; the answer carries a 16-character device token the exchange demands back, real whether the address exists or not; the code travels in the message as text to type — no link (`redirect_path` reserved out of the contract) | `modules/identity/onetimeaccess.TestRequestEmailAnswersTheSameForAnUnknownAddress`, `modules/identity/onetimeaccess.TestRequestEmailHoldsTheSendInsideTheCooldown`, `internal/transport.TestTheOneTimeAccessGuardIsDeclared` |

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
| POST | `/rpc/tango.authn.v1.MultifactorService/BeginTotpEnrollment` | Start TOTP enrollment | done — caller the handler resolves: a session, or the `mfa.required` enrollment bridge in the optional `pending_token` (an unusable bridge answers `unauthenticated`); answers the Base32 secret + otpauth URI exactly once; the secret is stored sealed (`enc:`); a second begin replaces the unconfirmed row | `modules/identity/multifactor` (service tests), `modules/identity/multifactor.TestTheRequiredGateRoutesAFactorlessSignInToEnrollment` |
| POST | `/rpc/tango.authn.v1.MultifactorService/ConfirmTotpEnrollment` | Confirm and enable TOTP | done — caller the handler resolves as its begin sibling; verifies one code against the enrollment; sets `confirmed_at`; answers the recovery codes exactly once | `modules/identity/multifactor.TestConfirmTotpEnrollmentActivatesOnTheRightCode`, `modules/identity/multifactor.TestTheRequiredGateRoutesAFactorlessSignInToEnrollment` |
| POST | `/rpc/tango.authn.v1.MultifactorService/ListTotpEnrollments` | List TOTP enrollments | done — self; the settings-page shape, never a secret; the decrypted-secret aid answers only behind the development exposure gate | `modules/identity/multifactor.TestListTotpEnrollmentsCarriesTheSecretOnlyWhereTheAidRuns` |
| POST | `/rpc/tango.authn.v1.MultifactorService/DeleteTotpEnrollment` | Delete one TOTP enrollment | done — self; the code field proves a held factor (another authenticator or a recovery code); deleting an unconfirmed row needs no proof | `modules/identity/multifactor.TestDeleteTotpEnrollmentProvesTheLastRemoval` |
| POST | `/rpc/tango.authn.v1.MultifactorService/CompleteSignIn` | Complete a pending sign-in | done — guard `Public`; spends the pending bridge a password sign-in minted (5-minute TTL, 3-wrong-codes budget) with a TOTP code or a recovery code; issues the full token pair | `modules/identity/multifactor.TestCompleteSignInOpensTheSessionOncePerCode`, `modules/identity/multifactor.TestCompleteSignInExhaustsTheBudgetAndRecoveryCodesStandIn` |
| POST | `/rpc/tango.authn.v1.MultifactorService/RegenerateRecoveryCodes` | Regenerate recovery codes | done — self; requires a held factor as the code field; the fresh set answers exactly once, the old set dies | `modules/identity/multifactor.TestRegenerateAndDisableRequireTheSecondFactor` |
| POST | `/rpc/tango.authn.v1.MultifactorService/DisableMfa` | Disable MFA | done — self; requires the second-factor proof; drops every authenticator and the recovery set | `modules/identity/multifactor.TestRegenerateAndDisableRequireTheSecondFactor` |
| POST | `/rpc/tango.authn.v1.MultifactorService/VerifyRecoveryCode` | Verify a recovery code | done — self; spends one code as a standalone identity proof, consumed exactly once | `modules/identity/multifactor.TestVerifyRecoveryCodeSpendsOneCodeStandalone` |
| POST | `/rpc/tango.authn.v1.MultifactorService/AdminDisableMfa` | Disable a user's MFA (admin) | done — guard `Admin`; the administrative session is the authority, no proof code; refuses an account with nothing confirmed (`failed_precondition`); a notice is queued to the account | `modules/identity/multifactor.TestAdminDisableMfaStripsEveryFactorWithoutAProof`, `modules/identity/multifactor.TestAdminDisableMfaRefusesAnUnknownAccount` |

Fixed parameters: issuer = the configured app name, 6 digits, 30-second period, SHA-1,
±1 step bounded skew. Sign-in composition: a confirmed TOTP enrollment turns a successful
password sign-in into a pending bridge (5-minute TTL, several live per account) instead of a
full session; the full session is issued only by `CompleteSignIn`. Pending state is
never a session flag, expires server-side, is replaced on the next sign-in, and is cleared on
sign-out. TOTP verification is constant-time with step replay protection (`last_used_step`);
recovery codes are hashed, single-use, shown exactly once, and rotated atomically. Disablement
requires the second-factor proof and clears every MFA row; the administrator's way in needs no
proof — the audit record names it.

## Webhooks (tango-only)

The outbound event surface tango carries and Pocket ID does not: an administrator registers a
destination and subscribes it to the event catalog. Every audit record is a candidate delivery,
mapped onto the dot-named catalog (`user.created`, `session.signed_in`, …); an endpoint that
lists no events, or lists the `*` wildcard, receives every one of them. All procedures carry the
admin guard.

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.webhook.v1.WebhookService/List` | List webhook endpoints | done — guard `Admin`; `enabled` and `event` filters; the `event` filter names a catalog event or the wildcard; secrets never present | `modules/webhook.TestEmissionMatchesSubscriptions`, `internal/guard` webhook rules |
| POST | `/rpc/tango.webhook.v1.WebhookService/Create` | Create a webhook endpoint | done — returns the signing secret exactly once; the secret is sealed `enc:` with the application cipher and a create without it is refused `failed_precondition`; subscription entries must be catalog names or `*` (`invalid_argument` otherwise) | `modules/webhook.TestCreateShowsTheSecretOnceAndRefusesADuplicateName`, `modules/webhook.TestCreateRejectsAnUnknownEvent` |
| POST | `/rpc/tango.webhook.v1.WebhookService/Get` | Get a webhook endpoint | done — no secret field; exercised by the create/rotate suites' read-back | `modules/webhook.TestRotateSecretAffectsNewDeliveriesOnly` |
| POST | `/rpc/tango.webhook.v1.WebhookService/Update` | Update a webhook endpoint | done — partial update; absent fields keep values; headers and event types replace wholesale when present | `modules/webhook.TestEmissionMatchesSubscriptions` (the disabled endpoint), `modules/webhook.TestCreateRejectsAnUnknownEvent` (event replacement) |
| POST | `/rpc/tango.webhook.v1.WebhookService/Delete` | Delete a webhook endpoint | done — deliveries survive with `webhook_id` nulled; a delivery whose endpoint is gone is marked failed, not retried | `modules/webhook.TestDeleteKeepsTheDeliveries` |
| POST | `/rpc/tango.webhook.v1.WebhookService/RotateSecret` | Rotate the signing secret | done — returns the new plaintext exactly once; new deliveries sign with it; the stored ciphertext is never answered again | `modules/webhook.TestRotateSecretAffectsNewDeliveriesOnly` |
| POST | `/rpc/tango.webhook.v1.WebhookService/Test` | Send a test delivery | done — queues a `webhook.test` delivery, subscription or not | `modules/webhook.TestTheTestDeliveryRidesItsOwnEvent` |
| POST | `/rpc/tango.webhook.v1.WebhookService/ListDeliveries` | List deliveries of one endpoint | done — newest first, paginated; latest attempt rides along | `modules/webhook.TestRunDeliverySignsTheBodyAndRecordsTheAttempt` |
| POST | `/rpc/tango.webhook.v1.WebhookService/ListAllDeliveries` | List all deliveries | done — `event` filter names a catalog event or the wildcard; redacted response metadata only | `modules/webhook.TestEmissionMatchesSubscriptions` |
| POST | `/rpc/tango.webhook.v1.WebhookService/ListEventTypes` | List webhook event types | done — guard `Admin`; serves the whole catalog with descriptions, in declaration order | E2E probe (2026-09-30): 77 entries over a freshly built binary |

Delivery contract: HMAC-SHA256 over `t=<unix>,v1=<hex>` where the digest covers the signed
timestamp concatenated with the exact canonical body bytes. Headers on every delivery:
`X-Signature` (timestamp + `v1` digest, ±5-minute verification skew), `X-Webhook-Event` (event
name), `X-Webhook-Id` (endpoint id), `Content-Type: application/json`. The canonical body is the
deterministic JSON encoding of the payload, capped at 1 MiB, stored once as immutable bytes and
reused byte-for-byte by every retry — the signature therefore stays valid across retries. Custom
registration headers cannot override the signature set, and their values are printable ASCII
without a line break. Subscriptions use catalog event names or the `*` wildcard; an empty list
receives every event.

Deliveries are sent on the queue (5 attempts, 30 s backoff, 30 s receiver deadline) and never
follow a redirect — a 3xx is the attempt's answer, so the signature headers never travel to a
redirect target. A redirect, a 4xx other than 408 and 429, a disabled or deleted endpoint, or a
host the destination policy refuses fails the delivery on its first answer; transport failures
and 5xx spend the retry budget. The destination policy is `webhook.allow_private_network`
(default `false`): a delivery whose host resolves to a loopback, private, link-local, or
unspecified address is refused — the SSRF guard an operator lifts only when its receivers
genuinely live beside the server. Retention runs on the recurring `webhook_prune` task: attempt
rows age out at 7 days, terminal deliveries at 30 (a pending delivery is never a candidate).
Rotation affects new deliveries only and never returns the stored ciphertext.

The event catalog (`modules/webhook/events.go`) maps every audit event onto one wire name — the
body's `event` field, the `X-Webhook-Event` header's value, and the subscription entry. The names
are curated, not derived: the audit vocabulary's snake_case cannot say where the first dot
belongs (`one_time_access_sign_in` would split as `one_time.access_sign_in`). The first segment
names the domain, the rest the happening in the past tense. An audit event without a mapping is
never emitted; a mapping is a subscription choice, never a rename. `webhook.test` is the one
entry no record causes.

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
| POST | `/rpc/tango.apikey.v1.ApiKeyService/ListAPIKeys` | List API keys | done — the caller's own keys, ordered by `name`, `created_at`, `expires_at`, or `last_used_at` (absent: newest first); revoked ones included; revoked stays listed because the revocation is a stamp the view carries, not a deletion | `modules/apikey.TestListOwnScopesToTheOwnerAndListAllSeesEverything` |
| POST | `/rpc/tango.apikey.v1.ApiKeyService/RenewAPIKey` | Renew API key | done — guard `Session`; an unexpired key is refused with `failed_precondition` (renewal is how a key lives past its expiry, not how it escapes one); an expired one earns a new secret and a new window, the reminder stamp dies with the old window, and the new raw key is shown once | `modules/apikey.TestRenewReplacesAnExpiredKeyAndRefusesALiveOne` |
| POST | `/rpc/tango.apikey.v1.ApiKeyService/RevokeAPIKey` | Revoke API key | done — guard `Session`; soft by the `revoked_at` stamp the schema reserved, idempotent (a second revocation is the same success and records nothing), and a key another account owns answers `not_found` | `modules/apikey.TestRevokeIsSoftAndIdempotent` |
| POST | `/rpc/tango.apikey.v1.ApiKeyService/ListAllAPIKeys` | List all API keys | done — guard `Admin`; tango-only, the administrative view over every key the deployment holds (upstream has none); same sort whitelist as the owner's list; the answer names each key's owner | `modules/apikey.TestListOwnScopesToTheOwnerAndListAllSeesEverything`, `internal/transport.TestTheAPIKeyGuardIsDeclared` |

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
| POST | `/rpc/tango.admin.v1.ApiService/ListApis` | List APIs | planned — no proto yet; upstream's API-access surface | — |
| POST | `/rpc/tango.admin.v1.ApiService/CreateAPI` | Create API | planned | — |
| POST | `/rpc/tango.admin.v1.ApiService/GetAPI` | Get API by ID | planned | — |
| POST | `/rpc/tango.admin.v1.ApiService/UpdateAPI` | Update API | planned | — |
| POST | `/rpc/tango.admin.v1.ApiService/DeleteAPI` | Delete API | planned | — |
| POST | `/rpc/tango.admin.v1.ApiService/SetPermissions` | Update API permissions | planned | — |
| POST | `/rpc/tango.admin.v1.ApiService/SetCimdAccess` | Update metadata document client access | planned | — |
| POST | `/rpc/tango.admin.v1.ApiService/ListAssignableClients` | List clients that can still be granted access | planned | — |
| POST | `/rpc/tango.admin.v1.ApiService/ListClients` | List clients with access to an API | planned | — |
| POST | `/rpc/tango.admin.v1.ApiService/GrantClient` | Grant a client access to an API | planned | — |
| POST | `/rpc/tango.admin.v1.ApiService/RevokeClient` | Revoke a client's access to an API | planned | — |
| POST | `/rpc/tango.admin.v1.ApiService/ListApisForClient` | List APIs a client may access | planned | — |
| POST | `/rpc/tango.admin.v1.ApiService/ListAssignableApisForClient` | List APIs a client can still be granted | planned | — |

Tango's machine credentials live in `ApiKeyService` (`modules/apikey`) above — this upstream
`ApiService` surface (API resources + client grants) is a separate, unbuilt feature.

## Application Configuration

| Method | Procedure / Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------------------- | -------------------- | ------ | -------- |
| GET | `/api/configuration` | Get application configuration | implemented — public route; anonymous and non-admin callers read the public subset (mode, base URL, sign-in and announcement toggles), an administrator's token widens the answer to every non-secret setting; a set secret is `[redacted]`, an unset one omitted, the datastore URLs reduced to `host:port/database` | `modules/appconfig/handler.go`, `internal/config/publish.go`, `internal/guard/rules.go` |
| POST | `/rpc/tango.system.v1.AppConfigService/TestEmail` | Send test email | implemented — admin; synchronous send to the caller's address on record, `to` redirects it | `modules/appconfig`, `internal/guard/rules.go` |
| PUT | `/api/application-configuration` | Update application configurations | excluded — the system configuration's source is the JSON file, resolved once at startup; there is no write surface | — |
| POST | `/api/application-configuration/sync-ldap` | excluded | — | — |

The configuration read is REST: one endpoint, `GET /api/configuration`, no
proto contract — the body is `config.Config.Published(full)` from
`internal/config/publish.go`, so the document cannot disagree with the types
it projects. The route is public on the guard's books and the REST bearer
middleware authenticates opportunistically; the handler reads the caller the
context carries and answers the wider document only to `IsAdministrator`. A
secret in the full document is the value through the redaction path
redact.go prints — `[redacted]`, the datastore URLs reduced to
`host:port/database`, an unset secret omitted — the same rendering
`config:print`'s fail-safe uses, never the value itself. The test-email
procedure is on `tango.system.v1.AppConfigService` in
`api/connect/system.proto`. The other SMTP checks stay with the mailer smoke
probe (`task mailer:smoke`).

## Settings

Tango-only surface — Pocket ID has no generic settings CRUD. The catalog in
code declares every item (key, default, sealed, public, description);
`public.settings` in `00005_create_platform_tables.sql` stores the overrides
alone. No delete surface exists by design: an item is removed by resetting
it, and a key not in the catalog is refused everywhere.

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.settings.v1.SettingsService/List` | List settings | implemented — admin; every catalog item with its effective value (override resting, else default) and the default it falls back to | `modules/appconfig/settings.go`, `internal/guard/rules.go` |
| POST | `/rpc/tango.settings.v1.SettingsService/Update` | Update setting | implemented — admin; catalog keys only; a sealed item seals the value (AES-256-GCM, `enc:` prefix) | `modules/appconfig/settings.go` |
| POST | `/rpc/tango.settings.v1.SettingsService/Reset` | Reset setting to default | implemented — admin; drops the override; an item already at its default answers unchanged | `modules/appconfig/settings.go` |
| POST | `/rpc/tango.settings.v1.SettingsService/ListPublic` | List public settings | implemented — public; only catalog items flagged `public`, names and values, never a sealed value; served from one cache entry a change drops, `nocache` in the body reads the source | `modules/appconfig/settings.go`, `internal/guard/rules.go` |

Whether a value rests sealed is told by its `enc:` prefix alone — there is
no flag column — and a public item never rests sealed: the catalog refuses
the pair at construction. Other features read through the `Settings`
service (`Get`, `GetString`, `GetBool`, `GetInt64`, `Update`, `Reset`),
which the appconfig area's `Package` provides; the RPC writes (`UpdateFor`,
`ResetFor`) record `setting_updated` / `setting_reset` in the causing
transaction, with the key in the payload and never the value.

## Application Images

| Method | Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------- | -------------------- | ------ | -------- |
| DELETE, GET, PUT | `/api/application-images/*` | Bundled application images | excluded — served from `public/images` through `/static/*` | — |
| GET | `/api/storage/sqlite-warning` | SQLite storage warning | excluded — Postgres is the only supported database | — |

## Audit Logs

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.auditlog.v1.AuditLogService/List` | List audit logs | done — the caller's own records; guard `Authenticated`, so a delegated (impersonated) caller is refused; sorted by `event`, `username`, `ip_address`, or `created_at` (absent: newest first) | `modules/auditlog` (service tests), `internal/transport.TestTheAuditListAnswersTheCallersOwnRecordsOnly` |
| POST | `/rpc/tango.auditlog.v1.AuditLogService/ListAll` | List all audit logs | done — guard `Admin`; filters `event`, `user_id`, `search` (username/email); sorted by `event`, `username`, `ip_address`, or `created_at` (absent: newest first) | `modules/auditlog` (service tests), `internal/transport.TestTheAdministrativeAuditProceduresAnswerAnAdministrator` |
| POST | `/rpc/tango.auditlog.v1.AuditLogService/ListForUser` | (tango-only) list one account's records | done — guard `Admin`; the administrative view of a single account | `modules/auditlog` (service tests) |
| POST | `/rpc/tango.auditlog.v1.AuditLogService/FilterOptions` | List filter facets | done — guard `Admin`; facets are `events` (distinct events in the table) and `users` (accounts that appear in it). Upstream's `client-names` facet is **not** ported: tango has no OIDC client, so `payload->>'client_name'` is never written | `modules/auditlog` (service tests), `internal/transport.TestTheAdministrativeAuditProceduresAnswerAnAdministrator` |

The writer is `internal/audit` (shared infrastructure, injected into the features); the reader is `modules/auditlog`. The event vocabulary lives in `internal/audit/audit.go` — that file is the single source of what can be written (sign-in/out and revocations, account and group lifecycle, email verification and change, one-time access, API keys, impersonation, MFA ceremonies, profile pictures); read it before adding a row that names an event. Retention: `app.audit_retention_days` (default 90) applied by the `audit_cleanup` recurring job.

## Custom Claims

Implemented in `modules/federation/customclaim` — the contract is
`tango.federation.v1.CustomClaimService` in `api/connect/federation.proto`
(the claims are a token-issuance concern, so the surface lives in the
federation area beside the clients the claims ride; the earlier plan's
`tango.identity.v1` namespace is superseded). A claim is unique per subject
(`(key, user_id, user_group_id)`, `NULLS NOT DISTINCT`), its value is a plain
string or a JSON document — the tokens carry it as the document it names —
and the identifiers travel as TypeIDs (`cclm_…`). The user and group
surfaces answer the same table, and each refuses a row that belongs to the
other subject kind, so a claim cannot silently move between subjects.
**Reserved keys** (settled 2026-09-29, mirroring Pocket ID v2.14.0's
`isReservedClaim` plus tango's own discriminator): the registered JWT claim
names (`sub`, `iss`, `aud`, `exp`, `iat`, `nbf`, `jti`, `auth_time`,
`nonce`, `acr`, `amr`, `azp`, `client_id`), the standard profile claims
tango emits (`given_name`, `family_name`, `name`, `display_name`,
`preferred_username`, `email`, `email_verified`, `groups`), and
`tango:token_type` are refused on create and update with `invalid_argument`.
The comparison is exact — a differently cased variant cannot collide with a
protected claim and is not blocked. At issuance the merge drops protected
keys a second time, so legacy rows predating the rule cannot overwrite
`sub`, `iss`, `aud`, the time claims, or the token-kind marker.

Upstream replaces a subject's whole claim set in one PUT; tango addresses
each row (create, update, delete by identifier) — the deviation
`.llms/tango-deviations.md` carries. The merge into the tokens happens at
issuance time (userinfo and the ID token), the same merge the preview
renders.

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.federation.v1.CustomClaimService/Suggest` | Get custom claim suggestions | done — admin; the keys in use, ordered by how often they carry | `modules/federation/customclaim.TestCreateListAndSuggestCoverTheLifecycle` |
| POST | `/rpc/tango.federation.v1.CustomClaimService/ListUserClaims` | List a user's custom claims | done — admin; ordered by key | `modules/federation/customclaim` (service tests) |
| POST | `/rpc/tango.federation.v1.CustomClaimService/CreateUserClaim` | Create a user custom claim | done — admin; duplicate key on the subject is `already_exists`, an unknown account is `failed_precondition`, a reserved key (registered JWT names, the standard profile claims, `tango:token_type`) is `invalid_argument` | `modules/federation/customclaim.TestCreateListAndSuggestCoverTheLifecycle`, `modules/federation/customclaim.TestReservedKeysAreRefusedOnCreateAndUpdate` |
| POST | `/rpc/tango.federation.v1.CustomClaimService/UpdateUserClaim` | Update a user custom claim | done — admin; full replace of key and value; a group claim is refused; a reserved key is `invalid_argument` | `modules/federation/customclaim.TestUpdateAndDeleteRefuseTheOtherSubjectKind`, `modules/federation/customclaim.TestReservedKeysAreRefusedOnCreateAndUpdate` |
| POST | `/rpc/tango.federation.v1.CustomClaimService/DeleteUserClaim` | Delete a user custom claim | done — admin; idempotent not-found | `modules/federation/customclaim.TestUpdateAndDeleteRefuseTheOtherSubjectKind` |
| POST | `/rpc/tango.federation.v1.CustomClaimService/ListGroupClaims` | List a user group's custom claims | done — admin; ordered by key | `modules/federation/customclaim` (service tests) |
| POST | `/rpc/tango.federation.v1.CustomClaimService/CreateGroupClaim` | Create a group custom claim | done — admin; the claim every member's tokens carry; a reserved key is `invalid_argument` | `modules/federation/customclaim` (service tests), `modules/federation/customclaim.TestReservedKeysAreRefusedOnCreateAndUpdate` |
| POST | `/rpc/tango.federation.v1.CustomClaimService/UpdateGroupClaim` | Update a group custom claim | done — admin; a user claim is refused; a reserved key is `invalid_argument` | `modules/federation/customclaim.TestUpdateAndDeleteRefuseTheOtherSubjectKind`, `modules/federation/customclaim.TestReservedKeysAreRefusedOnCreateAndUpdate` |
| POST | `/rpc/tango.federation.v1.CustomClaimService/DeleteGroupClaim` | Delete a group custom claim | done — admin | `modules/federation/customclaim` (service tests) |

Audit events: `custom_claim_created`, `custom_claim_updated`,
`custom_claim_deleted` — recorded in the causing transaction, the payload
naming the subject kind and the key, never the value.

## Device Login

Implemented in `modules/devicelogin` — the passkey-less pairing sign-in.
A browser that cannot sign itself in (a shared kiosk, a TV) creates a
pairing request; the account holder, signed in elsewhere, reads what the
request names and answers it. The device side is REST: the two routes a
browser reaches without a credential, the pairing secret riding an
http-only cookie the exchange demands back. The approval side is
ConnectRPC under `tango.authn.v1.DeviceApprovalService`, guarded
`Session` — a machine credential has no browser to pair — and the
handlers refuse an impersonated caller, so a token acting for another
cannot mint sessions for a third device.

The user code is eight characters of `23456789ABCDEFGHJKMNPQRSTUVWXYZ`
(no `0O1IL`), rendered `XXXX-XXXX`; the code and the pairing secret are
stored only as SHA-256 hashes. A request lives five minutes; the
exchange long-polls twenty-five seconds at a three-second rhythm.
Decisions are single-use in the UPDATE's WHERE: a request decided once
is decided forever, and of two concurrent exchanges exactly one consumes
the approval. Audit events: `device_login_approved`,
`device_login_denied`.

| Method | Procedure / Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------------------- | -------------------- | ------ | -------- |
| POST | `/api/device-login/requests` | Create device login request | done — REST, public; the pairing cookie rides the response, the device token never travels the body; one browser holds at most 8 live requests — a ninth creation answers 429 until one expires | `modules/devicelogin` (service tests), `internal/guard` (RestRules) |
| POST | `/api/device-login/requests/{id}/exchange` | Exchange device login request | done — REST, public; long-poll, the pairing cookie proves the creating browser; the approval answers the account view | `modules/devicelogin` (service tests) |
| POST | `/rpc/tango.authn.v1.DeviceApprovalService/Inspect` | Inspect device login request | done — guard `Session`; the code as typed, with or without its hyphen, in any case | `modules/devicelogin` (service tests) |
| POST | `/rpc/tango.authn.v1.DeviceApprovalService/Decide` | Decide device login request | done — guard `Session`; the impersonated caller refused; a repeat decision is the not-found | `modules/devicelogin` (service tests) |

## Health

| Method | Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------- | -------------------- | ------ | -------- |
| GET | `/healthz` | Responds to healthchecks | REST — liveness, dependencies untouched | `internal/transport.TestAPIHealthzReportsTheChecker` |
| GET | `/api/healthz` | Readiness document | REST — per-dependency results | `internal/transport.TestAPIHealthzReportsTheChecker` |
| POST | `/rpc/tango.system.v1.HealthService/Check` | Readiness over ConnectRPC | done — the same checker and the same result as `/api/healthz`; fails with `unavailable` naming the checks that are down | `internal/transport.TestRPCCheckAnswersTheReadinessDocument`, `internal/transport.TestRPCUnhealthyAnswersUnavailable` |

## OIDC

The provider surface is implemented across management, protocol, consent, and
CIMD. `tango.federation.v1.OidcClientService` and
`OidcConsentService` use ConnectRPC (`modules/federation/oidc`); the logo and
OAuth/OIDC protocol routes use REST. The endpoint table records active
implementation status and evidence; planned rows remain explicitly marked.

| Method | Procedure / Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------------------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.federation.v1.OidcClientService/ListClients` | List OIDC clients | done — admin; page/limit/search over the name; sorted by `id`, `name`, or `created_at` (absent: newest first); every client carries its secrets' views and its allowed groups | `modules/federation/oidc` (service tests), `internal/guard` (rules) |
| POST | `/rpc/tango.federation.v1.OidcClientService/CreateClient` | Create OIDC client | done — admin; the identifier is operator-chosen (letters, digits, `_`, `-`) or generated; the first secret is shown exactly once, only its SHA-256 hash stored in the `credentials` JSONB; a public client forces `pkce_enabled` on | `modules/federation/oidc.TestCreateMintsASecretTheRowCannotReplay` |
| POST | `/rpc/tango.federation.v1.OidcClientService/GetClient` | Get OIDC client | done — admin; the full view with secrets' views and groups | `modules/federation/oidc` (service tests) |
| POST | `/rpc/tango.federation.v1.OidcClientService/UpdateClient` | Update OIDC client | done — admin; full replace under the row lock; secrets, logo, and restriction untouched | `modules/federation/oidc.TestUpdateReplacesTheFieldsAndKeepsTheSecrets` |
| POST | `/rpc/tango.federation.v1.OidcClientService/DeleteClient` | Delete OIDC client | done — admin; the codes, sessions, grants, and restrictions die with the row by the cascades | `modules/federation/oidc.TestDeleteRemovesTheClientAndTheRecordNamesIt` |
| POST | `/rpc/tango.federation.v1.OidcClientService/UpdateAllowedUserGroups` | Update allowed user groups | done — admin; the replace, not a delta; an unknown group refuses the replacement whole | `modules/federation/oidc.TestAllowedGroupsReplaceWholeAndRefuseAnUnknownGroup` |
| POST | `/rpc/tango.federation.v1.OidcClientService/GetClientMeta` | Get client metadata | done — admin; the display facts a sign-in page renders | `modules/federation/oidc` (service tests) |
| POST | `/rpc/tango.federation.v1.OidcClientService/PreviewClient` | Preview OIDC client data for user | done — admin; the id-token, access-token, and userinfo claim maps built from the account's own views, no token minted; custom claims join when the customclaim feature does | `modules/federation/oidc.TestPreviewBuildsTheClaimMapsForTheAccount` |
| POST | `/rpc/tango.federation.v1.OidcClientService/RefreshClient` | Refresh client metadata document | done — admin; CIMD full: the id IS the metadata document's URL, the fetch is allowlist-gated (`oidc.cimd_url_allowlist`, judged again at refresh), the document's rules are held (auth method `none` only, an initiating grant required, `response_type` code only, redirect URIs without wildcards/script schemes); the refresh rewrites the document-named fields and sets `metadata_expires_at` | `modules/federation/oidc/cimd_test.go` |
| POST | `/rpc/tango.federation.v1.OidcClientService/UploadLogo` | Update client logo | done — admin; bytes payload, kind sniffed off the magic bytes (PNG/JPEG/WebP, 2 MiB; SVG refused — deviation), staged then synced in-request | `modules/federation/oidc.TestTheLogoLifecycleCoversTheKindCheckAndTheReset` |
| POST | `/rpc/tango.federation.v1.OidcClientService/DeleteLogo` | Delete client logo | done — admin; idempotent — a client without a logo is the same success | `modules/federation/oidc.TestTheLogoLifecycleCoversTheKindCheckAndTheReset` |
| POST | `/rpc/tango.federation.v1.OidcClientService/ListSecrets` | List client secrets | done — admin; prefixes and windows, values never returned | `modules/federation/oidc.TestSecretsAddWithdrawAndNeverReplayEachOther` |
| POST | `/rpc/tango.federation.v1.OidcClientService/CreateSecret` | Create client secret | done — admin; show-once raw value, SHA-256 hash + 4-character prefix stored in `credentials`; several live secrets are legitimate — a rotation is an addition followed by a deletion | `modules/federation/oidc.TestSecretsAddWithdrawAndNeverReplayEachOther` |
| POST | `/rpc/tango.federation.v1.OidcClientService/DeleteSecret` | Delete client secret | done — admin; one secret withdrawn, the others survive; an unknown one is not found | `modules/federation/oidc.TestSecretsAddWithdrawAndNeverReplayEachOther` |
| POST | `/rpc/tango.federation.v1.OidcConsentService/ListMyAuthorizedClients` | List authorized clients for current user | shipped — guard `Authenticated`, the caller's own ledger | `internal/guard` (rules) |
| POST | `/rpc/tango.federation.v1.OidcConsentService/RevokeMyAuthorizedClient` | Revoke authorization for an OIDC client | shipped — guard `Authenticated`; revocation cascades to the grants and the pointers riding them | `modules/federation/oidc.TestRevokingAConsentKillsTheGrantsAndTheirTokens` |
| POST | `/rpc/tango.federation.v1.OidcConsentService/ListMyClients` | List accessible OIDC clients for current user | shipped — guard `Authenticated`; the fail-closed restriction catalogue: a client counts as restricted when its flag is set or its allowed-groups roll carries rows, restricted clients answer only for their allowed groups' members | `modules/federation/oidc.TestTheAccessibleClientListFollowsTheGroupRestriction`, `modules/federation/oidc.TestTheCatalogueHidesAFlaggedClientWithNoGroups` |
| POST | `/rpc/tango.federation.v1.OidcConsentService/ListUserAuthorizedClients` | List authorized clients for a user | shipped — guard `Admin` | `internal/guard` (rules) |
| POST | `/rpc/tango.federation.v1.OidcConsentService/ListAllAuthorizedClients` | List every authorized client | shipped — guard `Admin` | `internal/guard` (rules) |
| GET | `/oidc/clients/{id}/logo` | Get client logo | done — REST, public; the raw image for the sign-in page, 404 for an unknown client or an absent logo, never a substitute | `modules/federation/oidc` (module mount), `internal/guard` (RestRules) |
| GET | `/oidc/authorize/{id}` | Resume the authorization interaction | done — REST, public; the callback the SPA's interaction page returns to once the account is signed in; without a decision it answers the interaction document (`consent_required`) | `modules/federation/oidc` (protocol mount, the `authorize/*` pattern) |
| POST | `/oidc/authorize/{id}` | Complete the authorization interaction | done — REST, public; the SPA posts the consent decision the flow grants scopes from | `modules/federation/oidc` (protocol mount, the `authorize/*` pattern) |
| GET, POST | `/oidc/authorize` | Authorization endpoint | done — REST, public, redirect and OAuth error contract | `modules/federation/oidc` (protocol mount) |
| POST | `/oidc/token` | Token endpoint | done — REST, public, form encoding, client authentication, RFC errors | `modules/federation/oidc` (protocol mount) |
| POST | `/oidc/introspect` | Introspect OIDC tokens | done — REST, client-scoped RFC 7662 (own tokens only) | `modules/federation/oidc` (protocol mount), protocol tests |
| POST | `/oidc/revoke` | Revoke OIDC tokens | done — REST, RFC 7009; client-scoped, an unknown token is a quiet 200, a stranger's token is 403 `access_denied`; the revocation marks the grant, the next refresh redemption answers `invalid_grant` and introspection answers `active: false` | `modules/federation/oidc` (protocol mount), `internal/guard` (RestRules, credential bucket) |
| POST | `/oidc/par` | Push authorization request | done — REST, RFC 9126; one-time request_uri, 5-minute lifetime | `modules/federation/oidc` (protocol mount), protocol tests |
| POST | `/oidc/device_authorization` | Device authorization grant | done — REST, public, RFC 8628; the codes resolve through hashed pointer rows | `modules/federation/oidc` (protocol mount), `internal/guard` (RestRules) |
| GET, POST | `/oidc/device` | Device verification | done — REST, public; the browser enters the user code and answers the consent question; the approval walks the SPA interaction | `modules/federation/oidc` (protocol mount) |
| GET, POST | `/oidc/end-session` | RP-initiated logout | done — REST, public; requires `id_token_hint`, refuses an `at+jwt` hint, revokes the account's grants and tokens for the client (the consent ledger too when the `oidc.end_session_revokes_consent` setting is on), delivers a back-channel logout token when the client registered a URI and the `oidc.backchannel_logout_enabled` setting is on, redirects to a registered `post_logout_redirect_uri` or the SPA root | `modules/federation/oidc` (protocol mount, `protocol_logout.go`, `backchannel.go`), `internal/guard` (RestRules) |
| GET, POST | `/oidc/userinfo` | Get user information | done — REST, public, bearer token, RFC-style errors | `modules/federation/oidc` (protocol mount) |

The protocol design notes below record implementation behavior for device flow,
PAR, end-session `id_token_hint` verification, and discovery. Tango's own JWKS
endpoint (`/.well-known/jwks.json`, `modules/identity/jwks`) is published by the
identity module; the federation provider uses that key set.

### Design contract — protocol core (slice 5), consent (6), device (7)

**Engine.** `github.com/luikyv/go-oidc` v0.25.0, mounted as REST under the
`/oidc` prefix via `provider.WithPathPrefix("/oidc")` + `Provider.Handler()` on
the transport router. `provider.New(goidcConfig)` reads: `Issuer` =
`app.base_url`, `JWKS` = the jwks service's key set (the same material
`/.well-known/jwks.json` publishes), `IDTokenAlgs` = the resolved signing
algorithm (`jwks.Service.SigningAlgorithm`), `Manager` = the grant manager.

**Endpoints (the specification's own shapes, never the envelope).**
Authorization = `GET,POST /oidc/authorize`; token = `POST /oidc/token`;
userinfo = `GET,POST /oidc/userinfo`; end-session = `GET,POST /oidc/end-session`;
the interaction continuation rides the authorization callback itself —
`GET,POST /oidc/authorize/{id}`, the session id the SPA's interaction page
carries back; introspect = `POST /oidc/introspect`; revoke = `POST /oidc/revoke`
(RFC 7009, the grant-wide mark); PAR = `POST /oidc/par`;
device = `POST /oidc/device_authorization` and the verification surface
`GET,POST /oidc/device[/{callback}]` (the library's default
names, its v0.25.0 API exposing no endpoint setter; the provider
registers its routes under `WithPathPrefix("/oidc")`); device login
pairs with it. Discovery at `/.well-known/openid-configuration` advertises
the issuer and protocol endpoints, the JWKS URI, supported grants/scopes/claims,
the introspection and revocation endpoints, PAR and device authorization
endpoints, and CIMD support. The RFC 8414 alias
`/.well-known/oauth-authorization-server` serves the same document — one
document cannot drift from itself.

**Storage.** The four managers map onto `oauth2_sessions` (`kind`, unique
`(kind,key)`, JSONB `request_data`, nullable `expires_at`): the object
rows — grants (`kind='grant'`, keyed by grant id), authorization sessions
(`kind='authn'`, PAR included), logout sessions (`kind='logout'`), device
sessions (`kind='device'`) — and one hashed **pointer row per presented
credential**: kinds `authcode`, `refresh`, `par`, `devicecode`, and
`usercode`, each keyed by the presented value's SHA-256 and carrying a
`tokenPointer` document that names the row it resolves to (plus the
client the FK checks). A consumed code loses its pointer, so a replay is
a not-found rather than a row. No `index_key` column exists — the
pointer rows are the secondary lookups. Rows expire per object
(`expires_at`): the grant's refresh window, the session's timeout, the
pointer's code lifetime; lookups refuse what the column judges dead and
the `protocol_cleanup` job reaps the rest (`internal/jobs`), the claimed
`oauth2_jtis` rows included — every jti a client-presented JWT carries
is claimed once there, a second presentation loses to the unique index. Client
resolution
(`DCRManager.Client`) reads `oidc_clients`: a standard client maps grant types
and the secret hashes to `goidc.Client`; an unknown `https://…` identifier
inside the CIMD allowlist materializes through the cimd feature first.

**Authorize flow.** `AuthnPolicy`'s authenticate callback answers
`StatusInProgress` whenever the browser carries no live account — the provider
redirects to the SPA's interaction page with the callback id; the SPA signs in
(or reuses its bearer) and resumes at `GET /oidc/authorize/{id}`, posting the
decision to the same callback with the scopes it consents to. Consent is
required when the client does not
skip it, the account has not authorized the client before, or `prompt=consent` —
first authorization writes `user_authorized_oidc_clients` (scope list +
`last_used_at`). The client's group restriction is judged at completion —
before any grant, consent, or device approval (`modules/federation/oidc`
`completeAuthentication`), fail-closed on the flag-or-roll rule. `pkce_supported`
is stamped when a client presents a
code challenge the requirement did not demand. PKCE is enforced for public
clients; `plain` and `S256` are accepted.

**Third-party initiated login.** A relying party links the browser
straight to `GET /oidc/authorize` with its own `client_id`, an optional
`login_hint` (a username or email), and `state` — the standard
authorization-request parameters the endpoint already accepts, no
separate entry point. The hint surfaces in the interaction document the
SPA receives (`login_hint` field, empty string when none) as a display
prefill; the signed-in credential, not the hint, decides the subject.
Every authorization response — success and error redirect alike —
carries the `iss` parameter (RFC 9207,
`authorization_response_iss_parameter_supported` in the discovery
document), so a page that framed the flow can tell this provider's
answer apart from any other landing on the same callback.

**Deferred: `check_session_iframe` and front-channel session
management.** The OP iframe's postMessage session polling waits for the
frontend's session-cookie story — the SPA owns the browser session
today, and a backend route without a frontend consumer answers nothing
the session RPCs (`ListSessions`, `RevokeSession`,
`SignOutAllSessions`) do not already cover. A decision someone makes,
not an accident; the discovery document does not advertise the field
until it ships. `TODO(frontend)`: the consent screen's `login_hint`
prefill.

**Token issuance.** Grants `authorization_code`, `refresh_token`, and
`urn:ietf:params:oauth:grant-type:device_code`, plus `client_credentials`
for the clients whose `allowed_grant_types` names it — the
machine-to-machine token names the client itself as its subject, carries
the client's requested scopes, no user claims and no refresh token, and
lives one hour (the `clientCredentialsLifetimeSecs` spelling). The
allowed list is per client: create and update carry
`allowed_grant_types` (the wire words the token endpoint judges; an
unknown word is refused, a CIMD client's list stays within its
document's declared grants), an absent or empty list rides the
registered default (authorization code, refresh, device), and the view
answers the list the client is judged by. Client authentication via
`client_secret_basic`,
`client_secret_post`, or `none` against hashed secrets in `credentials`; refresh
token rotation; access tokens are JWTs signed by the JWKS key set. Every
one-time grant's consumption is atomic under concurrency (settled 2026-09-29):
the token endpoint's save demands the stored grant still hold the document the
request read, so of the requests racing on one authorization code or device
code exactly one receives tokens — the losers answer `invalid_grant` (the
code already redeemed or gone) and never reach issuance. No token-type
discriminator claim is minted: the hint's identity rides the JOSE type
member instead — access tokens carry RFC 9068's `at+jwt` and ID tokens
carry none, and the end-session policy refuses a hint whose member reads
`at+jwt`. `tango:token_type` survives only as a reserved custom-claim
key. Scopes are `openid`, `profile`, `email`,
`groups`, and `offline_access` — the last one the persistent-access ask:
the grant that carries it rides the long refresh window the
`oidc.offline_refresh_token_hours` setting names (default 720 hours),
the other grants ride `oidc.refresh_token_hours` (default 336 hours;
zero on either is the never-expiring token the historical behavior
kept), both settings read fresh at every issuance and rotation. The
scope is consent-gated: the consent question names it like any other
requested word, and the SPA's consent screen renders it distinctly.
`TODO(frontend)`: the consent screen's offline_access line (persistent
access wording). Claims: `sub` always; `profile` adds the
custom claims plus `given_name`, `family_name`, `name`, `display_name`, and
`preferred_username`; `email` adds `email` and `email_verified` if an address
exists; `groups` adds group names. **Group restriction is enforced server-side**
(settled 2026-09-29): the authorization policy checks membership before any
grant, consent write, or device approval — a client counts as restricted when
its `is_group_restricted` flag is set or its allowed-groups roll carries rows,
a restricted client admits only accounts in an allowed group, and a flag with
an empty roll admits nobody. An ineligible account receives `access_denied`;
an existing consent does not bypass the gate. The consent catalogue
(`ListMyClients`) and the SCIM visibility roll follow the same rule.
**Expired state fails closed and is swept:** a lookup refuses any
`oauth2_sessions` row the `expires_at` column judges dead (two-minute
skew allowance), and the hourly `protocol_cleanup` job reaps the rows
past a one-hour grace in bounded batches — no `protocol` row outlives
its expiry by more than the sweep's interval.

**End-session.** `id_token_hint` is required — the provider holds no
browser session, so a request without one names no subject and answers
`invalid_request`. The hint is verified by the library (signature
against the JWKS, issuer, audience against `client_id` when the request
names both) and the policy refuses a hint whose JOSE type member is
`at+jwt` (an access token). A `post_logout_redirect_uri` that is not
registered on the client is refused with `invalid_request`, never
ignored. Success revokes the account's grants and tokens for the client
in one transaction — the authorized-client ledger dies with them when the
`oidc.end_session_revokes_consent` setting is on, survives otherwise (the
switch is a settings read per logout, so an operator's change lands
without a restart) — records
`oidc_session_ended`, and redirects to the registered URI with `state`
when one was given, to the SPA root otherwise.

**Back-channel logout delivers after the commit.** A client whose
`backchannel_logout_uri` is registered receives a logout token when an
end-session for it succeeds: signed by the provider's signing material
with `typ: logout+jwt`, `events` carrying the
`http://schemas.openid.net/event/backchannel-logout` member, `aud` the
client, `sub` the account, `iat`/`exp` a two-minute window, `jti` a
random draw, and `sid` the session identifier the hint carried — none
when the hint named none, and never a `nonce`. The delivery is the
`backchannel_logout` queue's job (`internal/jobs`): a form POST
(`logout_token=…`) expecting an empty 200, five attempts, one-minute
backoff — a failure logs and retries, it never fails the logout that
succeeded, and a token the queue redelivers after every attempt expired
is the client's safe refusal. The whole feature rides the
`oidc.backchannel_logout_enabled` setting (off by default), read per
logout like the consent switch. The client contract carries the fields:
`backchannel_logout_uri` (optional absolute URL) and
`backchannel_logout_session_required` on create, update, and the view.
`TODO(frontend)`: the client form needs fields for both, and a
grant-type editor for `allowed_grant_types` (checkbox list over the four
served words).

**Rate limits (guard's REST classification).** `/oidc/token`,
`/oidc/device/authorize`, `/oidc/par` ride the credential bucket
(`rate_limit.auth_limit`); `/oidc/authorize`, `/oidc/userinfo`,
`/oidc/introspect`, `/oidc/end-session`, and the interaction reads are
exempt. The classification moves to the REST routes, which the limiter's
tables will name directly.

**Switch.** `oidc.enabled` (default `true`) gates the `/oidc/*` mounts and the
discovery documents; off, they answer 404 while the management surface runs.

**Cleanup.** A recurring job (`protocol_cleanup`, the `audit_cleanup`
pattern, hourly) deletes expired `oauth2_sessions` rows past a one-hour
grace in bounded batches, and the lookup itself refuses a row the
`expires_at` column judges dead (see the retention note above).

## SCIM

Outbound provisioning, ported from Pocket ID's `internal/scimsync`: tango is the SCIM client, not
the server. One provider row per OIDC client names a remote base URL and the bearer token the
sync presents (sealed `enc:` at rest, shown once in the Create answer). One pass pushes the
client's visible accounts and groups out until the remote matches the local snapshot — the
visibility roll is the client's own (unrestricted = everyone, restricted = its allowed groups'
members), so provisioning cannot admit an account the sign-in would refuse. The pass also runs
hourly (`scim_sync` queue, `internal/jobs`) and, debounced five minutes, after account or group
changes (`scim_sync_notifier`). Schema: `public.scim_service_providers` (migration 00004, one
provider per client by unique index). Audit: `scim_provider_created/updated/deleted`,
`scim_sync_completed`. The listings the pass reads are validated before any write: a body that
is not a well-formed SCIM list, negative or contradictory counts, a cursor that never advances,
a listing beyond 100 pages / 100,000 resources, or a row without `id`/`externalId` fails the
pass without deletes — a malformed snapshot can never read as an empty one. A restriction with
no allowed groups legitimately deprovisions the remote whole. Matching is indexed by
`externalId` (O(local + remote)); `scimURL` joins by concatenation so an escaped id is never
re-encoded.

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.federation.v1.ScimProviderService/GetByClient` | Get SCIM service provider | done — admin; answers the provider one client syncs to, token always empty | `modules/federation/scimsync.TestAProviderRoundTripsThroughTheRepository` |
| POST | `/rpc/tango.federation.v1.ScimProviderService/Create` | Create SCIM service provider | done — admin; token shown once, sealed `enc:` at rest; a client with a provider answers failed-precondition; the client must exist | `modules/federation/scimsync.TestCreateSealsTheTokenAndAnswersItOnce` |
| POST | `/rpc/tango.federation.v1.ScimProviderService/Update` | Update SCIM service provider | done — admin; empty token keeps the stored one; the client binding is not replaceable | `modules/federation/scimsync.TestAProviderRoundTripsThroughTheRepository` |
| POST | `/rpc/tango.federation.v1.ScimProviderService/Delete` | Delete SCIM service provider | done — admin; the remote data the sync pushed stays where it is | `modules/federation/scimsync.TestAProviderRoundTripsThroughTheRepository` |
| POST | `/rpc/tango.federation.v1.ScimProviderService/Sync` | Sync SCIM service provider | done — admin; one pass now, counts in the answer; E2E-probed create/update/delete against a scripted remote, banned accounts push `active: false`, remote orphans are deleted | `modules/federation/scimsync.TestSyncProvisionsTheVisibleAccountsAndGroups` |

## User Groups

The groups accounts belong to. A group carries no permission of its own; the members it gathers
are addressed together, and a later feature may hang claims on a group or gate a client on it.
Every procedure is administrative — upstream guards the surface with its admin middleware — and
the member count every answer carries is what the query computes, never a column.

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.identity.v1.UserGroupService/ListUserGroups` | List user groups | done — the group's identifier travels in its wire form, the TypeID `ugrp_…` (`modules/identity/usergroup`), and a request that names a group without the prefix is refused; page/limit/search over the name and the display name; sorted by `name`, `display_name`, `user_count`, or `created_at`, ascending by default, the name columns case-insensitively; the LEFT join makes an empty group a row with a zero count, not an absence | `modules/identity/usergroup.TestListGroupsSearchesPaginatesAndSorts`, `internal/transport.TestTheUserGroupLoopEndsInAMemberList` |
| POST | `/rpc/tango.identity.v1.UserGroupService/GetUserGroup` | Get user group by ID | done — the detail carries the members as the account wire view, ordered by username, and the client allowlist the group names, ordered by the client's name (the parity read upstream's group view embeds) | `modules/identity/usergroup.TestCreateGroupStoresTheRowAndRefusesADuplicateName`, `modules/identity/usergroup.TestGroupDetailCarriesTheClientRoll` |
| POST | `/rpc/tango.identity.v1.UserGroupService/CreateUserGroup` | Create user group | done — the duplicate name is the unique index's answer read from the write's failure, mapped to `already_exists`; the created row is read back inside the transaction | `modules/identity/usergroup.TestCreateGroupStoresTheRowAndRefusesADuplicateName` |
| POST | `/rpc/tango.identity.v1.UserGroupService/UpdateUserGroup` | Update user group | done — full replace of the two fields; a name another group holds is refused and the other group stays intact; an unknown identifier is `not_found` | `modules/identity/usergroup.TestUpdateGroupReplacesTheFieldsAndRefusesADuplicate` |
| POST | `/rpc/tango.identity.v1.UserGroupService/DeleteUserGroup` | Delete user group | done — the membership rows die with the group by the foreign keys' cascade, the accounts are untouched; the record of the deletion names the member count it took away, the one fact a later reader cannot reconstruct | `modules/identity/usergroup.TestDeleteGroupRemovesTheMemberships` |
| POST | `/rpc/tango.identity.v1.UserGroupService/SetUserGroupMembers` | Update users in a group | done — the replace, not a delta: an empty list empties the group; every identifier must name an account, and a member that does not exist refuses the replacement whole, so the group keeps the set it held | `modules/identity/usergroup.TestSetMembersReplacesTheWholeSet` |
| POST | `/rpc/tango.identity.v1.UserGroupService/SetAllowedOidcClients` | Update allowed OIDC clients (group side) | done — admin; the group-side roll of the client restriction, the mirror of the client surface's `UpdateAllowedUserGroups`; the replace is whole, an unknown client refuses the replacement | `modules/identity/usergroup.TestSetAllowedOidcClientsReplacesTheRollAndRefusesAnUnknownClient` |
| POST | `/rpc/tango.identity.v1.UserGroupService/GetUserGroups` | Get user groups | done — the groups one account belongs to, ordered by display name; mirrors upstream `GET /api/users/{id}/groups` | `modules/identity/usergroup.TestGetUserGroupsAnswersTheAccountSMembership` |
| POST | `/rpc/tango.identity.v1.UserGroupService/UpdateUserGroups` | Update user groups | done — the whole-set replace of one account's memberships; every named group must exist, an unknown one refuses the replacement | `modules/identity/usergroup.TestUpdateUserGroupsReplacesAndRefusesTheUnknown` |

Audit events: `group_created`, `group_updated`, `group_deleted`,
`group_members_updated`, and `group_allowed_clients_updated` — the
membership change is its own event, because the log's one filter cannot see
inside a payload. Upstream records nothing for groups; tango records every
administrative write, the way it does for accounts. Not ported: the LDAP
guards (tango has no LDAP) and the custom claims a group carries (the
customclaim feature owns them).

## Users

| Method | Procedure / Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------------------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.identity.v1.SignupService/Signup` | Sign up | done — requires a valid signup token; password and names required; created unverified; answers the canonical account view | `modules/identity/signup` (service tests) |
| POST | `/rpc/tango.identity.v1.SignupService/GetSetupAvailability` | Check initial admin setup availability | excluded — the bootstrap travels the `tango initialize` CLI (release build), not an RPC procedure; a setup surface on the wire would be a public endpoint racing the operator for the first account | — |
| POST | `/rpc/tango.identity.v1.SignupService/SetupInitialAdmin` | Sign up initial admin user | excluded — see `GetSetupAvailability` above | — |
| POST | `/rpc/tango.identity.v1.SignupService/ListSignupTokens` | List signup tokens | done — admin Bearer; paginated; sorted by `created_at`, `expires_at`, `usage_count`, or `usage_limit` (absent: newest first); each token answers the groups its sign-ups join | `modules/identity/signup` (service tests) |
| POST | `/rpc/tango.identity.v1.SignupService/CreateSignupToken` | Create signup token | done — admin Bearer; raw token shown once; optional `user_group_ids` links the groups every account signed up under the token joins, checked to exist at issue time | `modules/identity/signup.TestSignupJoinsTheGroupsTheTokenCarried` |
| POST | `/rpc/tango.identity.v1.SignupService/DeleteSignupToken` | Delete signup token | done — admin Bearer | `modules/identity/signup` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/ListUsers` | List users | done — admin Bearer; paginated; optional search over username/email/display_name; sorted by `username`, `email`, `first_name`, `last_name`, `display_name`, or `created_at` (absent: newest first); every account view carries its group memberships | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/GetUser` | Get user by ID | done — admin Bearer; the view carries the group memberships | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/CreateUser` | Create user | done — admin Bearer; mandatory names; optional password (absent = no credential); optional `user_group_ids` joins the groups at creation — an unknown id rolls the whole creation back, and the answer carries the memberships | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/UpdateUser` | Update user | done — admin Bearer; full replace; mandatory names; ban fields as a unit; `timezone` reset to `UTC` when empty and refused when it names no zone the tz database carries | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/DeleteUser` | Delete user | done — admin Bearer; refuses the signed-in account | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/UpdateCurrentUser` | Update current user | done — guard `Authenticated`; full replace of the signed-in account's own profile fields — the names, the locale, and the timezone (IANA, validated with `time.LoadLocation`, empty resets to `UTC`) | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/GetCurrentUser` | Get current user | done — guard `Authenticated`; the signed-in account's view | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/DeleteMyAccount` | Delete own account | done — guard `Authenticated`; refused `not_found`-shaped while `users.self_delete_enabled` is off or the account's `self_delete_override` refuses (NULL defers to the global toggle); an impersonated caller is refused at the handler — a delegation may not end the account it wears; the real delete archives the row through the soft-delete trigger, and the audit names the account in `resource_type`/`resource_id` | `modules/identity/user` (service tests, `deleted_records` capture) |
| POST | `/rpc/tango.identity.v1.UserService/BanUser` | Ban a user | done — admin Bearer; the ban window rides the account's own row; audit `user_banned` | `modules/identity/user` (service tests) |
| POST | `/rpc/tango.identity.v1.UserService/UnbanUser` | Unban a user | done — admin Bearer; lifts the ban; audit `user_unbanned` | `modules/identity/user` (service tests) |
| PUT | `/api/users/{id}/profile-picture` | Update user profile picture | done — REST raw-body upload; self-service Bearer (guard `Self("id")` on the path param); magic-byte sniff (PNG/JPEG/WebP), max 2 MiB; stored at `avatars/<id>.<ext>` with the extension the sniffed bytes earn, so a kind change moves the key and deletes the replaced picture first; staged then synced in-request | `modules/identity/user` (service + handler tests), `internal/guard` (rule) |
| POST | `/rpc/tango.identity.v1.UserService/ResetProfilePicture` | Reset user profile picture | done — self-service Bearer (guard `Self("id")`); deletes the stored file and clears the row | `modules/identity/user` (service tests), `internal/guard` (rule), `internal/transport` (guard) |
| POST | `/rpc/tango.authn.v1.SessionService/ImpersonateUser` | Impersonate a user (admin) | done — guard `Admin`; opens a **new** session on the target account with the delegation recorded (`sessions.impersonated_by`, the access token's `ActorID`); refuses another administrator, the caller themselves, and an unknown account; audit `impersonation_started`; a delegated caller is refused on self-service procedures by the guard | `modules/identity/session.TestImpersonateUserOpensADelegatedSession`, `modules/identity/session.TestImpersonateUserRefusesAdminsAndItselfAndTheUnknown`, `internal/transport.TestTheGuardRefusesAnImpersonatedCallerOnASelfProcedure` |
| POST | `/rpc/tango.authn.v1.SessionService/StopImpersonating` | Stop impersonating | done — guard `Authenticated` (callable while the delegation is active); ends the delegated session and reissues the actor's own token pair; audit `impersonation_stopped` | `modules/identity/session.TestStopImpersonatingEndsTheDelegationAndReissuesTheActor`, `modules/identity/session.TestStopImpersonatingRefusesTheNonDelegatedAndTheForeign` |
| POST | `/rpc/tango.authn.v1.OneTimeAccessService/RequestEmail` | Request one-time access email | done — public; anti-enumeration: an unknown address answers the same success and a real device token; refused with `permission_denied` while `auth.one_time_access_email_as_unauthenticated_enabled` is off | `modules/identity/onetimeaccess.TestRequestEmailAnswersTheSameForAnUnknownAddress` |
| POST | `/rpc/tango.authn.v1.OneTimeAccessService/RequestEmailAsAdmin` | Request one-time access email (admin) | done — admin; refused with `permission_denied` while `auth.one_time_access_email_as_admin_enabled` is off; the code travels by email alone | `modules/identity/onetimeaccess.TestRequestEmailAsAdminSendsWithoutExposingTheCode` |
| POST | `/rpc/tango.authn.v1.OneTimeAccessService/CreateToken` | Create one-time access token for user (admin) | done — admin; the six-character code is the short window’s form; only the hash is stored, so the response is the last the code exists | `modules/identity/onetimeaccess.TestCreateTokenIssuesACodeTheExchangeAccepts` |
| POST | `/rpc/tango.authn.v1.OneTimeAccessService/ExchangeToken` | Exchange one-time access token | done — public; the code’s spend, the session, and the audit record commit in one transaction, so a rollback returns the code; a device token the email request paired with the code must come back exact | `modules/identity/onetimeaccess.TestExchangeRefusesADeviceTokenThatDoesNotMatch`, `internal/transport.TestTheOneTimeAccessLoopEndsInASession` |
| POST | `/rpc/tango.identity.v1.EmailVerificationService/SendEmail` | Send email verification | done — self-service Bearer; refuses verified; resend cooldown on last_sent_at; token row upserted, email via the durable queue | `modules/identity/verification` (service tests, Mailpit end-to-end) |
| POST | `/rpc/tango.identity.v1.EmailVerificationService/VerifyEmail` | Verify email | done — public; token is the credential; consumed on success | `modules/identity/verification` (service tests) |
| POST | `/rpc/tango.identity.v1.EmailVerificationService/RequestEmailChange` | Request email change | done — self-service Bearer; the token row binds the pending address (`auth_tokens.payload`); refused `already_exists` when another account holds the address and `failed_precondition` when it is the current one; resend cooldown; confirm-code email to the NEW address is transactional, the pending notice to the OLD address is gated by `mailer.notifications.email_change_notice_enabled` | `modules/identity/verification/email_change_test.go` |
| POST | `/rpc/tango.identity.v1.EmailVerificationService/ConfirmEmailChange` | Confirm email change | done — public; token is the credential; the move, the verified stamp, and the token delete commit in one transaction; uniqueness re-judged inside it, so a lost race leaves the token unconsumed; success notice to the new address is gated | `modules/identity/verification/email_change_test.go` |
| GET | `/api/users/{id}/profile-picture.png` | Get user profile picture | done — REST; public; streams the stored bytes, an account without one answers the bundled default by redirect to `/images/default-avatar.png` | `modules/identity/user` (handler test) |
| GET | `/api/uploads/{key}` | Get upload progress | done — REST; guard `Authenticated`; the manifest's own state (`key`, `status` `pending`\|`ready`\|`failed`, byte `size`) — the engine stages whole files, so there is no per-chunk distance; a key nothing stored answers not_found; the finished transition also rides the `AfterSyncHook` into an `upload_finished_notice` addressed to the staging metadata's `owner` | `internal/transport.TestUploadProgressAnswersTheManifestState`, `internal/transport.TestUploadProgressRefusesAnUnauthenticatedCaller`, `internal/jobs.TestUploadFinishedProcessorTellsTheOwner` |

## WebAuthn

Implemented in `modules/identity/webauthn` — the passkey surface: one credential in three roles
(passwordless first factor, the MFA bridge's second factor, the step-up proof). The ceremonies
travel as opaque JSON: the requests carry the browser's `PublicKeyCredential` JSON verbatim and
the answers carry the server's options JSON verbatim, so the client never re-shapes what the
browser produced. The `credential` field is a **string** carrying that JSON — not an object.
Enrollment ceilings (`passkey.max_credentials`, `mfa.max_enrollments`) and the
`webauthn.allow_synced_passkeys` toggle are catalog settings judged at the verify call, live.
An account left with no way in at all refuses the removal — the recovery anchor is the
invariant every delete door holds.

Guard: the ceremonies split at the token line — the sign-in side is `Public` (the credential is
the credential it judges), the management side is `Session` (an impersonated caller is refused,
so an administrator cannot plant a credential on the account they are wearing), and the
administrative roll doors ride the permission catalog.

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.authn.v1.WebAuthnService/BeginRegistration` | Begin passkey registration | done — guard `Session`; answers the creation options JSON and the ceremony handle (`wcs_…`, one minute); the unverified enrollment row is the ceremony state | `modules/identity/webauthn` (service tests, soft authenticator) |
| POST | `/rpc/tango.authn.v1.WebAuthnService/VerifyRegistration` | Verify passkey registration | done — guard `Session`; verifies the attestation against the stored challenge, records the counter and backup flags; the limits are judged here, an empty name falls back to the AAGUID's model name; a failed verification costs the ceremony | `modules/identity/webauthn.TestEnrollmentEndsInACredential`, `TestEnrollmentBeyondTheCredentialLimitIsRefused`, `TestEnrollmentRefusesASyncedPasskeyWhenTheToggleIsOff` |
| POST | `/rpc/tango.authn.v1.WebAuthnService/BeginLogin` | Begin passkey sign-in | done — public; empty `allowCredentials`, the discoverable sign-in: the account resolves from the credential the browser presents | `modules/identity/webauthn` (service tests) |
| POST | `/rpc/tango.authn.v1.WebAuthnService/VerifyLogin` | Verify passkey sign-in | done — public; the assertion with user verification is full authentication: the session issues in one step, no bridge, even on an MFA-enabled account; a rewound counter answers `failed_precondition` (the clone refusal, silent by design) | `modules/identity/webauthn.TestSignInEndsInAWholeSession`, `TestAClonedCredentialIsRefused` |
| POST | `/rpc/tango.authn.v1.WebAuthnService/ListCredentials` | List passkeys | done — guard `Session`; the caller's roll, oldest first, no key material | `modules/identity/webauthn` (service tests) |
| POST | `/rpc/tango.authn.v1.WebAuthnService/UpdateCredential` | Rename passkey | done — guard `Session`; another account's credential answers not-found, the same refusal an unknown id earns | `modules/identity/webauthn` (service tests) |
| POST | `/rpc/tango.authn.v1.WebAuthnService/DeleteCredential` | Delete passkey | done — guard `Session` + step-up (`X-Tango-Reauthentication`); deleting the last credential is allowed while the password row exists | `modules/identity/webauthn` (service tests) |
| POST | `/rpc/tango.authn.v1.WebAuthnService/Reauthenticate` | Reauthenticate (step-up proof) | done — guard `Session`; proves the caller by password or passkey assertion and answers a single-use token (five minutes, hashed at rest); the guarded call spends it through the `X-Tango-Reauthentication` header; three wrong proofs do not apply — the proof is one grant, judged once | `modules/identity/webauthn.TestAStepUpProofSpendsOnce`, `internal/transport` (interceptor consumption) |
| POST | `/rpc/tango.authn.v1.WebAuthnService/AdminListCredentials` | List user passkeys (admin) | done — guard `Admin`; the roll over a named account — Pocket ID's mirror `GET /api/users/{id}/webauthn-credentials` | `modules/identity/webauthn.TestAdminSeesAnotherAccountsRoll` |
| POST | `/rpc/tango.authn.v1.WebAuthnService/AdminUpdateCredential` | Rename user passkey (admin) | done — guard `Admin`; the holder's rules over any account; audit `webauthn_credential_admin_renamed` | `modules/identity/webauthn.TestAdminRenamesAnotherAccountsPasskey` |
| POST | `/rpc/tango.authn.v1.WebAuthnService/AdminDeleteCredential` | Delete user passkey (admin) | done — guard `Admin`; the stranding refusal holds here too — Pocket ID's mirror `DELETE /api/users/{id}/webauthn-credentials/{credentialId}`; audit `webauthn_credential_admin_removed` | `modules/identity/webauthn.TestAdminDeleteRefusesTheLastWayIn` |

Expired ceremony rows and the spent proof tokens are swept hourly by the `webauthn_cleanup` job
(`internal/jobs`). The AAGUID catalog ships embedded (`aaguid.json`, names only) and names an
unnamed credential's authenticator. The browser ladder runs on Playwright (`e2e-tests/`, the
virtual authenticator) and the wire-level driver is `tools/e2e-passkey` — both drive the debug
build's simulation pages at `/debug/passkey/*` (Utilities below).

## Version

| Method | Procedure | Summary / Yaak Title | Status | Evidence |
| ------ | --------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.system.v1.VersionService/Current` | Get current deployed version | planned — no proto yet; `system.proto` holds only `HealthService` | — |
| POST | `/rpc/tango.system.v1.VersionService/Latest` | Get latest available version | planned — anonymous; falls back to the deployed build when the feed never answered | — |

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
| GET | `/.well-known/jwks.json` | Get JSON Web Key Set (JWKS) | REST — done; bare RFC 7517 JWK Set over the configured key pair + `public.jwks` signing rows, cached behind `jwtutils.KeyProvider` | `modules/identity/jwks` (handler + integration tests) |
| GET | `/.well-known/oauth-authorization-server` | Get OAuth 2.0 authorization server metadata | done — REST, RFC 8414; the discovery document served at the alias path, revocation and introspection surfaces included | `modules/federation/oidc` (protocol mount, `requestAt` alias) |
| GET | `/.well-known/openid-configuration` | Get OpenID Connect discovery configuration | done — REST; the provider's discovery document: issuer, endpoints, scopes (`openid`, `profile`, `email`, `groups`, `offline_access`), `authorization_response_iss_parameter_supported` (RFC 9207 on), revocation and introspection surfaces | `modules/federation/oidc` (protocol mount) |

## Notifications

| Method | Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.notification.v1.NotificationService/CreateNotification` | [Tango] Create notification | done — Admin; publishes to `global`/`users`/`user_groups` audience, validates the named accounts and groups exist, refuses a system notice with a topic or a global audience; email pass gated by `mailer.notifications.announcement_email_enabled` | `modules/notification` |
| POST | `/rpc/tango.notification.v1.NotificationService/GetNotification` | [Tango] Get notification | done — Admin; full view with the audience junctions named | `modules/notification` |
| POST | `/rpc/tango.notification.v1.NotificationService/ListAllNotifications` | [Tango] List all notifications | done — Admin; page + `sort_by`/`sort_order` + `category` filter; cancelled stay listed | `modules/notification` |
| POST | `/rpc/tango.notification.v1.NotificationService/CancelNotification` | [Tango] Cancel notification | done — Admin; stamps `cancelled_at`, idempotent, the row survives | `modules/notification` |
| POST | `/rpc/tango.notification.v1.NotificationService/ListNotifications` | [Tango] List notifications | done — Authenticated; the caller's inbox resolved through the shared visibility predicate, `read_at` per row, `unread_only`/`category` filters, `sort_by`/`sort_order` | `modules/notification` |
| POST | `/rpc/tango.notification.v1.NotificationService/MarkNotificationRead` | [Tango] Mark notification read | done — Authenticated; receipt written once, `not_found` for what the caller is not targeted by | `modules/notification` |
| POST | `/rpc/tango.notification.v1.NotificationService/MarkAllNotificationsRead` | [Tango] Mark all notifications read | done — Authenticated; one INSERT..SELECT over the visibility predicate, answers how many it marked | `modules/notification` |
| POST | `/rpc/tango.notification.v1.NotificationService/UnreadCount` | [Tango] Unread count | done — Authenticated; the bell-badge number | `modules/notification` |
| POST | `/rpc/tango.notification.v1.NotificationService/WatchNotifications` | [Tango] Watch notifications (stream) | done — Authenticated; server-streaming, in-process broker, ping keepalive (immediate + 25s), no history — catch-up via List; deadline exemption via `middleware.UnboundedFor` | `modules/notification` + `internal/transport/router_rpc.go` |

Identifiers are TypeIDs on the wire, the columns stay UUIDs: a notification is `ntf_…` (`modules/notification.IDFromUUID`/`UUIDFromWire`), the audience accounts and groups arrive and leave as `usr_…`/`ugrp_…`, and `created_by` renders in the account's wire form.

## Authorization

| Method | Endpoint | Summary / Yaak Title | Status | Evidence |
| ------ | -------- | -------------------- | ------ | -------- |
| POST | `/rpc/tango.authz.v1.AuthorizationService/ListPermissions` | [Tango] List permissions | done — Admin; the code-declared catalog read-only (`perm_…` id, slug, description; search, resource filter, sort by slug/description); cached under the query's fingerprint, a role write drops the family, `nocache` reads the source | `modules/identity/authorization` + `internal/authz` |
| POST | `/rpc/tango.authz.v1.AuthorizationService/ListRoles` | [Tango] List roles | done — Admin; page + `search` + `sort_by`/`sort_order` (name, slug, created_at) + `type` filter (system/custom), `permission_count` per row; cached under the query's fingerprint, a role write drops the family, `nocache` reads the source | `modules/identity/authorization` |
| POST | `/rpc/tango.authz.v1.AuthorizationService/GetRole` | [Tango] Get role | done — Admin; the row plus the permission slugs it carries; cached by id, a role write drops the family, `nocache` reads the source | `modules/identity/authorization` |
| POST | `/rpc/tango.authz.v1.AuthorizationService/CreateRole` | [Tango] Create role | done — Admin; unique name and slug, born with no permissions | `modules/identity/authorization` |
| POST | `/rpc/tango.authz.v1.AuthorizationService/UpdateRole` | [Tango] Update role | done — Admin; name + description only; slug and type immutable; a system role refuses | `modules/identity/authorization` |
| POST | `/rpc/tango.authz.v1.AuthorizationService/DeleteRole` | [Tango] Delete role | done — Admin; custom roles only, refused while accounts hold it | `modules/identity/authorization` |
| POST | `/rpc/tango.authz.v1.AuthorizationService/SetRolePermissions` | [Tango] Set role permissions | done — Admin; replaces the set; every slug must be cataloged; a system role refuses | `modules/identity/authorization` |
| POST | `/rpc/tango.authz.v1.AuthorizationService/ListUserRoles` | [Tango] List user roles | done — Admin; the account's active roles | `modules/identity/authorization` |
| POST | `/rpc/tango.authz.v1.AuthorizationService/SetUserRoles` | [Tango] Set user roles | done — Admin; replaces the set; leaving grants are revoked (stamped, not deleted) | `modules/identity/authorization` |
| POST | `/rpc/tango.authz.v1.AuthorizationService/ListUserPermissions` | [Tango] List user permissions | done — Admin; the account's direct grants, outside any role | `modules/identity/authorization` |
| POST | `/rpc/tango.authz.v1.AuthorizationService/SetUserPermissions` | [Tango] Set user permissions | done — Admin; replaces the direct grants; every slug must be cataloged | `modules/identity/authorization` |

Permissions are identified by slug everywhere (the catalog is code: `internal/authz`, `resource:id:action`, `*` in the instance position only); the catalog rows also carry a `perm_…` TypeID, which names the entry on the wire but never replaces the slug as the grant handle. Roles are `role_…` TypeIDs, accounts `usr_…`. The grants an access token carries are a snapshot taken at mint time — a change here lands at the next sign-in or refresh. `users.is_admin` is gone: an administrator is an account the `administrator` system role holds, and `tango initialize` bootstraps one on a fresh database (the system seed plus the first administrator, refused against a database that already holds an account); `tango admin:reset-password` recovers an administrator's access.
