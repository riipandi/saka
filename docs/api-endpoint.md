# API Endpoints

The surfaces the server answers, reconciled against the code on 2026-09-27. The agent-facing
matrix with per-row status and test evidence is `.llms/endpoint-reference.md`; the code is the
only record of what is served.

Every ConnectRPC procedure is called with `POST` under `/rpc`; the wire is snake_case JSON. REST
routes live under `/api` (or their root path) and are throttled with the same policy.

## Authentication (tango-only)

Password authentication is a tango-only surface; upstream Pocket ID signs users in with passkeys
only. `SignIn` takes
`{"identity": "<username or email>", "password": "<plaintext>", "remember": <bool>}` and answers
the token pair plus the session view; `remember` selects the long or short session lifetime. A
confirmed TOTP enrollment turns a successful sign-in into a pending bridge instead —
`MultifactorService/CompleteSignIn` spends it (see MFA below).

| Method   | Procedure / Endpoint                                         | Protocol     | Summary                             |
| -------- | ------------------------------------------------------------ | ------------ | ----------------------------------- |
| POST     | `/rpc/tango.auth.v1.AuthService/SignIn`                      | ConnectRPC   | Sign in with password               |
| POST     | `/rpc/tango.auth.v1.SessionService/Refresh`                  | ConnectRPC   | Refresh the token pair (rotates the refresh token) |
| POST     | `/rpc/tango.auth.v1.SessionService/GetSession`               | ConnectRPC   | Inspect current session             |
| POST     | `/rpc/tango.auth.v1.SessionService/ListSessions`             | ConnectRPC   | List own sessions                   |
| POST     | `/rpc/tango.auth.v1.SessionService/RevokeSession`            | ConnectRPC   | Revoke one own session              |
| POST     | `/rpc/tango.auth.v1.SessionService/SignOut`                  | ConnectRPC   | Sign out                            |
| POST     | `/rpc/tango.auth.v1.SessionService/SignOutOtherSessions`     | ConnectRPC   | Sign out other sessions             |
| POST     | `/rpc/tango.auth.v1.SessionService/SignOutAllSessions`       | ConnectRPC   | Sign out all sessions               |
| POST     | `/rpc/tango.auth.v1.SessionService/ImpersonateUser`          | ConnectRPC   | Impersonate a user (admin)          |
| POST     | `/rpc/tango.auth.v1.SessionService/StopImpersonating`        | ConnectRPC   | Stop impersonating                  |

## Password Recovery (tango-only)

| Method   | Procedure / Endpoint                                         | Protocol     | Summary                             |
| -------- | ------------------------------------------------------------ | ------------ | ----------------------------------- |
| POST     | `/rpc/tango.auth.v1.PasswordRecoveryService/ForgotPassword`  | ConnectRPC   | Request a password reset (anti-enumeration) |
| POST     | `/rpc/tango.auth.v1.PasswordRecoveryService/ResetPassword`   | ConnectRPC   | Reset with the emailed token        |
| POST     | `/rpc/tango.auth.v1.PasswordRecoveryService/AdminResetUserPassword` | ConnectRPC | Trigger a reset for one account (admin) |

The token is 256 bits of lowercase hex — URL-safe with no special characters — delivered by
email and stored only as a hash.

## One-Time Access

A one-time access code signs an account in without its password. Codes are drawn from an
alphabet without ambiguous characters — six characters for a code that lives fifteen minutes or
less, twelve above — and stored as SHA-256 hashes. The email paths are gated by
`auth.one_time_access_email_as_admin_enabled` and
`auth.one_time_access_email_as_unauthenticated_enabled`, both off by default.

| Method   | Procedure / Endpoint                                                        | Protocol     | Summary                                     |
| -------- | --------------------------------------------------------------------------- | ------------ | ------------------------------------------- |
| POST     | `/rpc/tango.auth.v1.OneTimeAccessService/CreateToken`                        | ConnectRPC   | Create one-time access token for user (admin) |
| POST     | `/rpc/tango.auth.v1.OneTimeAccessService/ExchangeToken`                      | ConnectRPC   | Exchange one-time access token               |
| POST     | `/rpc/tango.auth.v1.OneTimeAccessService/RequestEmailAsAdmin`                | ConnectRPC   | Request one-time access email (admin)        |
| POST     | `/rpc/tango.auth.v1.OneTimeAccessService/RequestEmail`                       | ConnectRPC   | Request one-time access email                |

## MFA TOTP

Tango-only; upstream Pocket ID has no TOTP. A confirmed enrollment turns a successful password
sign-in into a pending bridge (5-minute TTL, 3-wrong-codes budget) that only `CompleteSignIn`
completes — with a TOTP code or a recovery code.

| Method   | Procedure / Endpoint                                         | Protocol     | Summary                      |
| -------- | ------------------------------------------------------------ | ------------ | ---------------------------- |
| POST     | `/rpc/tango.auth.v1.MultifactorService/BeginTotpEnrollment`  | ConnectRPC   | Start TOTP enrollment (secret shown once, sealed at rest) |
| POST     | `/rpc/tango.auth.v1.MultifactorService/ConfirmTotpEnrollment`| ConnectRPC   | Confirm and enable TOTP (recovery codes shown once) |
| POST     | `/rpc/tango.auth.v1.MultifactorService/ListTotpEnrollments`  | ConnectRPC   | List the account's authenticators (never a secret) |
| POST     | `/rpc/tango.auth.v1.MultifactorService/DeleteTotpEnrollment` | ConnectRPC   | Delete one authenticator (second-factor proof) |
| POST     | `/rpc/tango.auth.v1.MultifactorService/CompleteSignIn`       | ConnectRPC   | Complete a pending sign-in   |
| POST     | `/rpc/tango.auth.v1.MultifactorService/RegenerateRecoveryCodes` | ConnectRPC | Rotate recovery codes (shown once) |
| POST     | `/rpc/tango.auth.v1.MultifactorService/DisableMfa`           | ConnectRPC   | Disable MFA (second-factor proof) |
| POST     | `/rpc/tango.auth.v1.MultifactorService/VerifyRecoveryCode`   | ConnectRPC   | Spend one recovery code as a standalone proof |
| POST     | `/rpc/tango.auth.v1.MultifactorService/AdminDisableMfa`      | ConnectRPC   | Disable a user's MFA (admin, no proof needed) |

## API Key

A machine credential a holder issues for their own scripting and integrations. It acts as its
owner; the one surface a key is refused on is the keys' own (`Session` guard rule). Shown once,
stored as a SHA-256 hash of the presented `<prefix>.<secret>` string.

| Method   | Procedure / Endpoint                                                        | Protocol     | Summary          |
| -------- | --------------------------------------------------------------------------- | ------------ | ---------------- |
| POST     | `/rpc/tango.apikey.v1.ApiKeyService/CreateAPIKey`                            | ConnectRPC   | Create API key   |
| POST     | `/rpc/tango.apikey.v1.ApiKeyService/ListAPIKeys`                             | ConnectRPC   | List API keys    |
| POST     | `/rpc/tango.apikey.v1.ApiKeyService/RenewAPIKey`                             | ConnectRPC   | Renew API key    |
| POST     | `/rpc/tango.apikey.v1.ApiKeyService/RevokeAPIKey`                            | ConnectRPC   | Revoke API key   |
| POST     | `/rpc/tango.apikey.v1.ApiKeyService/ListAllAPIKeys`                          | ConnectRPC   | List all API keys (tango-only, administrative) |

## Audit Logs

Read-only by construction: no procedure writes, edits, or deletes a record. The event vocabulary
lives in `internal/audit/audit.go`; retention is a scheduled job, not an endpoint.

| Method   | Procedure / Endpoint                                            | Protocol     | Summary                              |
| -------- | --------------------------------------------------------------- | ------------ | ------------------------------------ |
| POST     | `/rpc/tango.auditlog.v1.AuditLogService/List`                   | ConnectRPC   | List the caller's own audit logs     |
| POST     | `/rpc/tango.auditlog.v1.AuditLogService/ListAll`                | ConnectRPC   | List all audit logs (admin)          |
| POST     | `/rpc/tango.auditlog.v1.AuditLogService/ListForUser`            | ConnectRPC   | List one account's audit logs (admin) |
| POST     | `/rpc/tango.auditlog.v1.AuditLogService/FilterOptions`          | ConnectRPC   | List filter facets (admin)           |

## Users

| Method   | Procedure / Endpoint                                                         | Protocol     | Summary                                         |
| -------- | ---------------------------------------------------------------------------- | ------------ | ----------------------------------------------- |
| POST     | `/rpc/tango.identity.v1.UserService/GetCurrentUser`                          | ConnectRPC   | The account the caller is                       |
| POST     | `/rpc/tango.identity.v1.UserService/UpdateCurrentUser`                       | ConnectRPC   | Update the signed-in account's own profile      |
| POST     | `/rpc/tango.identity.v1.UserService/ListUsers`                               | ConnectRPC   | List users (admin)                              |
| POST     | `/rpc/tango.identity.v1.UserService/GetUser`                                 | ConnectRPC   | Get user by ID (admin)                          |
| POST     | `/rpc/tango.identity.v1.UserService/CreateUser`                              | ConnectRPC   | Create user (admin)                             |
| POST     | `/rpc/tango.identity.v1.UserService/UpdateUser`                              | ConnectRPC   | Update user (admin)                             |
| POST     | `/rpc/tango.identity.v1.UserService/DeleteUser`                              | ConnectRPC   | Delete user (admin; refuses the signed-in account) |
| POST     | `/rpc/tango.identity.v1.UserService/BanUser`                                 | ConnectRPC   | Ban a user (admin)                              |
| POST     | `/rpc/tango.identity.v1.UserService/UnbanUser`                               | ConnectRPC   | Unban a user (admin)                            |
| POST     | `/rpc/tango.identity.v1.UserService/ResetProfilePicture`                     | ConnectRPC   | Reset user profile picture                      |
| PUT      | `/api/users/me/profile-picture`                                              | HTTP/REST    | Update own profile picture (raw-body upload)    |
| DELETE   | `/api/users/me/profile-picture`                                              | HTTP/REST    | Reset own profile picture                       |
| PUT      | `/api/users/{id}/profile-picture`                                            | HTTP/REST    | Update a user's profile picture (self-service)  |
| GET      | `/api/users/{id}/profile-picture.png`                                        | HTTP/REST    | Get user profile picture (public)               |

## Sign-up and Signup Tokens

| Method   | Procedure / Endpoint                                                         | Protocol     | Summary                                         |
| -------- | ---------------------------------------------------------------------------- | ------------ | ----------------------------------------------- |
| POST     | `/rpc/tango.identity.v1.SignupService/Signup`                                | ConnectRPC   | Sign up (requires a signup token)               |
| POST     | `/rpc/tango.identity.v1.SignupService/ListSignupTokens`                      | ConnectRPC   | List signup tokens (admin)                      |
| POST     | `/rpc/tango.identity.v1.SignupService/CreateSignupToken`                     | ConnectRPC   | Create signup token (admin; raw token shown once) |
| POST     | `/rpc/tango.identity.v1.SignupService/DeleteSignupToken`                     | ConnectRPC   | Delete signup token (admin)                     |

## User Groups

A group carries no permission of its own; the members it gathers are addressed together. The
membership update is a replace: the request names the whole member set.

| Method   | Procedure / Endpoint                                                             | Protocol     | Summary                           |
| -------- | -------------------------------------------------------------------------------- | ------------ | --------------------------------- |
| POST     | `/rpc/tango.identity.v1.UserGroupService/ListUserGroups`                          | ConnectRPC   | List user groups (admin)          |
| POST     | `/rpc/tango.identity.v1.UserGroupService/GetUserGroup`                            | ConnectRPC   | Get user group by ID (admin)      |
| POST     | `/rpc/tango.identity.v1.UserGroupService/CreateUserGroup`                         | ConnectRPC   | Create user group (admin)         |
| POST     | `/rpc/tango.identity.v1.UserGroupService/UpdateUserGroup`                         | ConnectRPC   | Update user group (admin)         |
| POST     | `/rpc/tango.identity.v1.UserGroupService/DeleteUserGroup`                         | ConnectRPC   | Delete user group (admin)         |
| POST     | `/rpc/tango.identity.v1.UserGroupService/SetUserGroupMembers`                     | ConnectRPC   | Update users in a group (admin)   |
| POST     | `/rpc/tango.identity.v1.UserGroupService/GetUserGroups`                           | ConnectRPC   | List one user's groups            |
| POST     | `/rpc/tango.identity.v1.UserGroupService/UpdateUserGroups`                        | ConnectRPC   | Replace one user's groups (admin) |

## Email Verification and Email Change

| Method   | Procedure / Endpoint                                                         | Protocol     | Summary                                         |
| -------- | ---------------------------------------------------------------------------- | ------------ | ----------------------------------------------- |
| POST     | `/rpc/tango.identity.v1.EmailVerificationService/SendEmail`                  | ConnectRPC   | Send email verification                          |
| POST     | `/rpc/tango.identity.v1.EmailVerificationService/VerifyEmail`                | ConnectRPC   | Verify email (token is the credential)           |
| POST     | `/rpc/tango.identity.v1.EmailVerificationService/RequestEmailChange`         | ConnectRPC   | Request email change (confirm link to the new address) |
| POST     | `/rpc/tango.identity.v1.EmailVerificationService/ConfirmEmailChange`         | ConnectRPC   | Confirm email change (token is the credential)   |

## Health Check

| Method   | Procedure / Endpoint                               | Protocol     | Summary                    |
| -------- | -------------------------------------------------- | ------------ | -------------------------- |
| GET      | `/healthz`                                         | HTTP/REST    | Liveness; touches no dependency |
| GET      | `/api/healthz`                                     | HTTP/REST    | Readiness document         |
| POST     | `/rpc/tango.system.v1.HealthService/Check`         | ConnectRPC   | Readiness over ConnectRPC  |

## Well Known

| Method   | Service / Endpoint                          | Protocol    | Summary                                       |
| -------- | ------------------------------------------- | ----------- | --------------------------------------------- |
| GET      | `/.well-known/jwks.json`                    | HTTP/REST   | JSON Web Key Set (bare RFC 7517 JWK Set)      |

## Infrastructure

Non-API routes the server mounts. They serve the deployment and the SPA, not the application
contract.

| Method   | Endpoint                | Protocol   | Summary                                    |
| -------- | ----------------------- | ---------- | ------------------------------------------ |
| GET      | `/`                     | HTTP/REST  | SPA document; every unmatched path falls back to it |
| GET      | `/api/`                 | HTTP/REST  | API root document (name, version, platform) |
| GET      | `/static/*`             | HTTP/REST  | Embedded static assets, plus served uploads |

Debug-build-only utilities (`/debug/do`, `/debug/encode-id`, `/debug/decode-id`) answer `404` in
a release build.

## Planned (not implemented)

The scaffolds below exist as empty packages waiting for their phase; **no proto, no routes** —
every row is uncallable. The designed surfaces live in `.llms/endpoint-reference.md`.

| Feature | Scaffold | Surfaces planned |
| ------- | -------- | ---------------- |
| WebAuthn passkeys | `modules/identity/webauthn` (kept deliberately) | `/api/webauthn/{register,login}/{begin,finish}` + `UserService` passkey procedures |
| OIDC federation | `modules/federation/{oidc,discovery}` | `OidcClientService`, `OidcConsentService`, `/authorize`, `/api/oidc/*`, discovery documents |
| SCIM sync | `modules/federation/scimsync` | `ScimProviderService` |
| Custom claims | `modules/federation/customclaim` | `CustomClaimService` |
| Device login | `modules/devicelogin` | `/api/device-login/*` + `DeviceApprovalService` |
| Application configuration | `modules/appconfig` | `ApplicationConfigurationService` + `/api/application-configuration` |
| Webhooks | `modules/webhook` | `WebhookService` |
| API resources (upstream `ApiService`) | — | `tango.admin.v1.ApiService` |
| Version metadata | — | `tango.system.v1.VersionService` |
| Initial admin setup | — | `SignupService/GetSetupAvailability`, `SetupInitialAdmin` |
