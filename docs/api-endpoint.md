# API Endpoint

Summary of the public API surface, grouped by area.

## Reference

This document is self-contained: the tables below are the authoritative description of every route
the server mounts, and a route that is not listed is not served.

Two conventions apply throughout:

- **Upstream parity** — a section notes where the surface diverges from Pocket ID. Sections marked
  *tango-only* have no upstream equivalent; the rest mirror the upstream contract so a Pocket ID
  client can be pointed at this server.
- **Transport** — an operation is served either as ConnectRPC below `/rpc` or as HTTP/REST. The
  protocol column states which, and the two never overlap for the same operation.

## Transport and authentication

- **ConnectRPC** below `/rpc` — the SPA, the admin console, and internal tools call the generated
  clients from `api/connect/*.proto`. Every procedure is called with `POST` and
  `Content-Type: application/json` plus `Connect-Protocol-Version: 1`; `GET` is reserved for
  procedures that declare `idempotency_level = NO_SIDE_EFFECTS`, and no procedure does. Errors use
  the Connect code/message document.
- **HTTP/REST** below `/api`, `/authorize`, or a documented root path — protocol and infrastructure
  surfaces only: OAuth/OIDC, WebAuthn ceremonies, device-login request and exchange, email links,
  the auth worker's token refresh, health, and discovery.
- **Credentials** — `Authorization: Bearer <access-token>` for protected RPCs. Machine clients may
  also send `X-API-KEY` on the admin application API only; every self-service and
  credential-lifecycle surface refuses a machine credential, and API key create and renew stay
  session-only so a leaked key cannot extend itself or rotate its owner's password. Cookies are
  token storage and never authorize an RPC. Browser ceremony surfaces (device approval, MFA, the
  auth lifecycle) never accept a machine credential.

## Lifetimes

Every authentication lifetime is configured through the environment, in
**seconds**, and validated at startup: a non-positive value, or a short session
longer than the remembered one, fails startup instead of degrading at runtime.

| Variable | Bounds |
| --- | --- |
| `AUTH_ACCESS_TOKEN_EXPIRY` | internal RPC bearer JWT; refresh uses the session token |
| `AUTH_SESSION_LIFETIME` | session issued with `remember: true` |
| `AUTH_SESSION_SHORT_LIFETIME` | session issued with `remember: false` or absent |
| `OIDC_ACCESS_TOKEN_EXPIRY` | provider default; a client's own duration overrides it |
| `OIDC_REFRESH_TOKEN_EXPIRY` | provider default; a client's own duration overrides it |
| `OIDC_AUTHORIZATION_CODE_EXPIRY` | one-time authorization code |
| `OIDC_INTERACTION_EXPIRY` | sign-in / consent interaction bridge |
| `OIDC_DEVICE_CODE_EXPIRY` | device authorization window (RFC 8628) |
| `OIDC_PAR_EXPIRY` | pushed authorization `request_uri` (RFC 9126) |

## Authentication

Password authentication is a tango-only surface; upstream Pocket ID signs users in with passkeys
only. Recovery stays on HTTP: the `ForgotPassword` and `ResetPassword` RPCs answer `unimplemented`.
Reading and updating the caller's own profile live on `UserService` (`GetSession` for the read),
matching upstream's `GET`/`PUT /api/users/me`; the account surface carries only the tango-only
password and session procedures.

`SignIn` takes `{"identity": "<username or email>", "password": "<plaintext>", "remember": <bool>}`.
The `session_token` field below is spelled `refresh_token` on the wire, and the procedures live
under `tango.auth.v1.AuthService` and `tango.auth.v1.SessionService` — the authentication package,
not `tango.identity.v1`.
`remember` selects the session duration: `true` issues the long lifetime
(`AUTH_SESSION_LIFETIME`), `false` or absent the short one
(`AUTH_SESSION_SHORT_LIFETIME`). Both are seconds and must be configured so the
short lifetime does not exceed the long one. The choice is stored on the session, so a sliding
refresh or token rotation never promotes a short session to the long lifetime. The
response echoes the mode as `remember`.

There are no cookies. The session-issuing procedures (`SignIn`, `Signup`,
`SetupInitialAdmin`, and `VerifyPending`) return the credentials in the `SignedIn` body:

| Field           | Meaning                                                                 |
| --------------- | ----------------------------------------------------------------------- |
| `session_token` | The rotating session credential. Absent while a second factor is pending. |
| `access_token`  | The short-lived internal bearer JWT. Absent while a second factor is pending. |
| `pending_token` | The bridge credential for the pending second factor. Present only when `pending` is true. |

Present `session_token` to `POST /api/auth/token` (body `{"session_token": "..."}`) to mint a
fresh bearer; the answer carries `access_token` and the replacement `session_token`, because the
presented one stops resolving. Send the bearer on every protected call. `VerifyPending` takes
`{"code": "...", "pending_token": "..."}`.

| Method   | Procedure / Endpoint                                         | Protocol     | Summary                             |
| -------- | ------------------------------------------------------------ | ------------ | ----------------------------------- |
| POST     | `/rpc/tango.identity.v1.AuthService/SignIn`                  | ConnectRPC   | Sign in with password               |
| POST     | `/rpc/tango.identity.v1.AuthService/SignOut`                 | ConnectRPC   | Sign out                            |
| POST     | `/rpc/tango.auth.v1.SessionService/SignOutOtherSessions`     | ConnectRPC   | Sign out other sessions             |
| POST     | `/rpc/tango.auth.v1.SessionService/SignOutAllSessions`       | ConnectRPC   | Sign out all sessions               |
| POST     | `/rpc/tango.identity.v1.AuthService/GetSession`              | ConnectRPC   | Inspect current session, including the caller's user |
| POST     | `/rpc/tango.identity.v1.AuthService/ForgotPassword`          | ConnectRPC   | Request a password reset (unimplemented; use the REST route) |
| POST     | `/rpc/tango.identity.v1.AuthService/ResetPassword`           | ConnectRPC   | Reset a password (unimplemented; use the REST route) |
| POST     | `/rpc/tango.identity.v1.AccountService/ChangePassword`       | ConnectRPC   | Change own password                 |
| POST     | `/rpc/tango.identity.v1.AccountService/ListSessions`         | ConnectRPC   | List own sessions                   |
| POST     | `/rpc/tango.identity.v1.AccountService/RevokeSession`        | ConnectRPC   | Revoke one own session              |
| POST     | `/api/auth/token`                                            | HTTP/REST    | Refresh: session token to access bearer |
| POST     | `/api/auth/forgot-password`                                  | HTTP/REST    | Request a password reset            |
| POST     | `/api/auth/reset-password`                                   | HTTP/REST    | Reset with a reset token            |

## One-Time Access

A one-time access code signs an account in without its password. An administrator issues a code
for one account — whose credential is lost, or not yet set — or sends it by email; an account
holder asks for the email from the sign-in page. The exchange answers the token pair a password
sign-in answers with, under a session whose provider is `one_time_access`.

The code is a credential shown once: only its SHA-256 hash is stored, and the unique index on
`(user_id, purpose)` keeps an account to one code at a time, so a re-request re-issues rather
than stacks. Codes are drawn from an alphabet without ambiguous characters — six characters when
they live fifteen minutes or less, twelve above — because a holder types them from a phone
reading an email.

Two configuration switches gate the email paths, both off by default:
`auth.one_time_access_email_as_admin_enabled` and
`auth.one_time_access_email_as_unauthenticated_enabled`. The public one is off for a reason: it
turns the deployment's mailer into something anyone on the internet can drive at any address,
and the anti-enumeration answer (an unknown address succeeds identically) is the only thing that
keeps it from becoming an address oracle.

The exchange is the procedure the frontend reaches with the code the email linked to
(`/login-code?code=…`): the frontend holds the code from the link — and the device token the
email request answered, when there was one — and forwards both here. The email path pairs the
code with a 16-character device token the requester holds, so a code read out of a mailbox alone
signs nobody in; a mismatch leaves the code spendable, because the caller's mistake is not the
code's spend.

| Method   | Procedure / Endpoint                                                        | Protocol     | Summary                                     |
| -------- | --------------------------------------------------------------------------- | ------------ | ------------------------------------------- |
| POST     | `/rpc/tango.auth.v1.OneTimeAccessService/CreateToken`                        | ConnectRPC   | Create one-time access token for user (admin) |
| POST     | `/rpc/tango.auth.v1.OneTimeAccessService/ExchangeToken`                      | ConnectRPC   | Exchange one-time access token               |
| POST     | `/rpc/tango.auth.v1.OneTimeAccessService/RequestEmailAsAdmin`                | ConnectRPC   | Request one-time access email (admin)        |
| POST     | `/rpc/tango.auth.v1.OneTimeAccessService/RequestEmail`                       | ConnectRPC   | Request one-time access email                |

## MFA TOTP

Tango-only; upstream Pocket ID has no TOTP. A confirmed enrollment turns a successful password
sign-in into a pending authentication that only `VerifyPending` completes.

| Method   | Procedure / Endpoint                                         | Protocol     | Summary                      |
| -------- | ------------------------------------------------------------ | ------------ | ---------------------------- |
| POST     | `/rpc/tango.identity.v1.MfaService/EnrollTotp`               | ConnectRPC   | Start TOTP enrollment        |
| POST     | `/rpc/tango.identity.v1.MfaService/ConfirmTotp`              | ConnectRPC   | Confirm and enable TOTP      |
| POST     | `/rpc/tango.identity.v1.MfaService/GetTotpStatus`            | ConnectRPC   | TOTP status                  |
| POST     | `/rpc/tango.identity.v1.MfaService/VerifyPending`            | ConnectRPC   | Complete a pending sign-in   |
| POST     | `/rpc/tango.identity.v1.MfaService/RotateRecoveryCodes`      | ConnectRPC   | Rotate recovery codes        |
| POST     | `/rpc/tango.identity.v1.MfaService/DisableTotp`              | ConnectRPC   | Disable TOTP                 |

## OAuth

Relying-party protocol surfaces. Client administration moved to ConnectRPC (see OIDC).

| Method      | Procedure / Endpoint                      | Protocol     | Summary                                  |
| ----------- | ----------------------------------------- | ------------ | ---------------------------------------- |
| GET, POST   | `/authorize`                              | HTTP/REST    | Authorization endpoint                   |
| POST        | `/api/oidc/token`                         | HTTP/REST    | Token endpoint                           |
| POST        | `/api/oidc/introspect`                    | HTTP/REST    | Introspect OIDC tokens (RFC 7662)        |
| POST        | `/api/oidc/par`                           | HTTP/REST    | Push authorization request (RFC 9126)    |
| POST        | `/api/oidc/device/authorize`              | HTTP/REST    | Device authorization grant (RFC 8628)    |
| GET         | `/api/oidc/device/info`                   | HTTP/REST    | Device code info for the consent page    |
| POST        | `/api/oidc/device/verify`                 | HTTP/REST    | Approve or deny a device code            |
| GET, POST   | `/api/oidc/userinfo`                      | HTTP/REST    | Get user information                     |
| GET, POST   | `/api/oidc/end-session`                   | HTTP/REST    | RP-initiated logout                      |
| GET         | `/api/oidc/interaction/{id}`              | HTTP/REST    | Read the authorization interaction       |
| POST        | `/api/oidc/interaction/{id}/approve`      | HTTP/REST    | Approve the authorization interaction    |
| GET         | `/api/oidc/clients/{clientId}/logo`       | HTTP/REST    | Get client logo                          |

## WebAuthn

Passkey ceremonies are a browser contract and stay on HTTP.

| Method   | Procedure / Endpoint              | Protocol    | Summary                             |
| -------- | --------------------------------- | ----------- | ----------------------------------- |
| POST     | `/api/webauthn/register/begin`    | HTTP/REST   | Begin passkey registration          |
| POST     | `/api/webauthn/register/finish`   | HTTP/REST   | Finish passkey registration         |
| POST     | `/api/webauthn/login/begin`       | HTTP/REST   | Begin discoverable passkey login    |
| POST     | `/api/webauthn/login/finish`      | HTTP/REST   | Finish discoverable passkey login   |

## API Key

An API key is a machine credential a holder issues for their own scripting and integrations. It
acts as its owner: the claims it carries are the owner's account read live on every request, so an
administrator's key administers and a disabled account's keys open nothing from the moment the
flag moves. The one surface a key is refused on is the keys' own — a credential that cannot revoke
itself must not be the one managing credentials — which is what the `Session` guard rule is for.

The key is shown once, in the create and renew responses, and stored only as a SHA-256 hash of the
presented string, so a database leak cannot replay it. The presentation form is
`<prefix>.<secret>`: an eight-character prefix an operator reads (it travels in every view, so a
leaked key can be identified without the key itself) and a thirty-two character secret a client
sends. The name is unique per owner. The expiry reminder is off by default
(`auth.expiry_email_enabled`): a mailer that writes to account holders on a schedule is a
decision, not a default.

| Method   | Procedure / Endpoint                                                        | Protocol     | Summary          |
| -------- | --------------------------------------------------------------------------- | ------------ | ---------------- |
| POST     | `/rpc/tango.apikey.v1.ApiKeyService/CreateAPIKey`                            | ConnectRPC   | Create API key   |
| POST     | `/rpc/tango.apikey.v1.ApiKeyService/ListAPIKeys`                             | ConnectRPC   | List API keys    |
| POST     | `/rpc/tango.apikey.v1.ApiKeyService/RenewAPIKey`                             | ConnectRPC   | Renew API key    |
| POST     | `/rpc/tango.apikey.v1.ApiKeyService/RevokeAPIKey`                            | ConnectRPC   | Revoke API key   |
| POST     | `/rpc/tango.apikey.v1.ApiKeyService/ListAllAPIKeys`                          | ConnectRPC   | List all API keys (tango-only, administrative) |

## Application Configuration

Configure application settings.

| Method   | Procedure / Endpoint                                                         | Protocol     | Summary                                  |
| -------- | ---------------------------------------------------------------------------- | ------------ | ---------------------------------------- |
| POST     | `/rpc/tango.admin.v1.ApplicationConfigurationService/Get`                    | ConnectRPC   | Public bootstrap configuration           |
| POST     | `/rpc/tango.admin.v1.ApplicationConfigurationService/GetAll`                 | ConnectRPC   | List all application configurations      |
| POST     | `/rpc/tango.admin.v1.ApplicationConfigurationService/Update`                 | ConnectRPC   | Update application configurations        |
| POST     | `/rpc/tango.admin.v1.ApplicationConfigurationService/TestEmail`              | ConnectRPC   | Send test email                          |
| GET      | `/api/application-configuration`                                             | HTTP/REST    | List public application configurations   |

## Audit Logs

Read the audit trail. Records are written by the feature that caused them, in the same
transaction, and the surface here is read-only by construction: no procedure writes, edits,
or deletes a record. Retention is a scheduled job, not an endpoint.

| Method   | Procedure / Endpoint                                            | Protocol     | Summary                              |
| -------- | --------------------------------------------------------------- | ------------ | ------------------------------------ |
| POST     | `/rpc/tango.auditlog.v1.AuditLogService/List`                   | ConnectRPC   | List the caller's own audit logs     |
| POST     | `/rpc/tango.auditlog.v1.AuditLogService/ListAll`                | ConnectRPC   | List all audit logs (admin)          |
| POST     | `/rpc/tango.auditlog.v1.AuditLogService/ListForUser`            | ConnectRPC   | List one account's audit logs (admin) |
| POST     | `/rpc/tango.auditlog.v1.AuditLogService/FilterOptions`          | ConnectRPC   | List filter facets (admin)           |

## Custom Claim

Manage custom claims for users and groups.

| Method   | Procedure / Endpoint                                                      | Protocol     | Summary                             |
| -------- | ------------------------------------------------------------------------- | ------------ | ----------------------------------- |
| POST     | `/rpc/tango.identity.v1.CustomClaimService/Suggest`                       | ConnectRPC   | Get custom claim suggestions        |
| POST     | `/rpc/tango.identity.v1.CustomClaimService/ListUserClaims`                | ConnectRPC   | List a user's custom claims         |
| POST     | `/rpc/tango.identity.v1.CustomClaimService/CreateUserClaim`               | ConnectRPC   | Create a user custom claim          |
| POST     | `/rpc/tango.identity.v1.CustomClaimService/UpdateUserClaim`               | ConnectRPC   | Update a user custom claim          |
| POST     | `/rpc/tango.identity.v1.CustomClaimService/DeleteUserClaim`               | ConnectRPC   | Delete a user custom claim          |
| POST     | `/rpc/tango.identity.v1.CustomClaimService/ListGroupClaims`               | ConnectRPC   | List a user group's custom claims   |
| POST     | `/rpc/tango.identity.v1.CustomClaimService/CreateGroupClaim`              | ConnectRPC   | Create a group custom claim         |
| POST     | `/rpc/tango.identity.v1.CustomClaimService/UpdateGroupClaim`              | ConnectRPC   | Update a group custom claim         |
| POST     | `/rpc/tango.identity.v1.CustomClaimService/DeleteGroupClaim`              | ConnectRPC   | Delete a group custom claim         |

## OIDC

OIDC client administration and consent records.

| Method   | Procedure / Endpoint                                                                | Protocol     | Summary                                         |
| -------- | ----------------------------------------------------------------------------------- | ------------ | ----------------------------------------------- |
| POST     | `/rpc/tango.federation.v1.OidcClientService/ListClients`                            | ConnectRPC   | List OIDC clients                               |
| POST     | `/rpc/tango.federation.v1.OidcClientService/CreateClient`                           | ConnectRPC   | Create OIDC client                              |
| POST     | `/rpc/tango.federation.v1.OidcClientService/GetClient`                              | ConnectRPC   | Get OIDC client                                 |
| POST     | `/rpc/tango.federation.v1.OidcClientService/UpdateClient`                           | ConnectRPC   | Update OIDC client                              |
| POST     | `/rpc/tango.federation.v1.OidcClientService/DeleteClient`                           | ConnectRPC   | Delete OIDC client                              |
| POST     | `/rpc/tango.federation.v1.OidcClientService/UpdateAllowedUserGroups`                | ConnectRPC   | Update allowed user groups                      |
| POST     | `/rpc/tango.federation.v1.OidcClientService/GetClientMeta`                          | ConnectRPC   | Get client metadata                             |
| POST     | `/rpc/tango.federation.v1.OidcClientService/PreviewClient`                          | ConnectRPC   | Preview OIDC client data for user               |
| POST     | `/rpc/tango.federation.v1.OidcClientService/RefreshClient`                          | ConnectRPC   | Refresh client metadata document                |
| POST     | `/rpc/tango.federation.v1.OidcClientService/UploadLogo`                             | ConnectRPC   | Update client logo                              |
| POST     | `/rpc/tango.federation.v1.OidcClientService/DeleteLogo`                             | ConnectRPC   | Delete client logo                              |
| POST     | `/rpc/tango.federation.v1.OidcClientService/ListSecrets`                            | ConnectRPC   | List client secrets                             |
| POST     | `/rpc/tango.federation.v1.OidcClientService/CreateSecret`                           | ConnectRPC   | Create client secret                            |
| POST     | `/rpc/tango.federation.v1.OidcClientService/DeleteSecret`                           | ConnectRPC   | Delete client secret                            |
| POST     | `/rpc/tango.federation.v1.OidcClientService/GetScimProvider`                        | ConnectRPC   | Get SCIM service provider for a client          |
| POST     | `/rpc/tango.federation.v1.OidcConsentService/ListMyAuthorizedClients`               | ConnectRPC   | List authorized clients for current user        |
| POST     | `/rpc/tango.federation.v1.OidcConsentService/RevokeMyAuthorizedClient`              | ConnectRPC   | Revoke authorization for an OIDC client         |
| POST     | `/rpc/tango.federation.v1.OidcConsentService/ListMyClients`                         | ConnectRPC   | List accessible OIDC clients for current user   |
| POST     | `/rpc/tango.federation.v1.OidcConsentService/ListUserAuthorizedClients`             | ConnectRPC   | List authorized clients for a user              |
| POST     | `/rpc/tango.federation.v1.OidcConsentService/ListAllAuthorizedClients`              | ConnectRPC   | List every authorized client                    |

## Users

User management, sign-up, one-time access, email verification, and profile pictures.

| Method   | Procedure / Endpoint                                                         | Protocol     | Summary                                         |
| -------- | ---------------------------------------------------------------------------- | ------------ | ----------------------------------------------- |
| POST     | `/rpc/tango.identity.v1.SignupService/Signup`                                | ConnectRPC   | Sign up                                         |
| POST     | `/rpc/tango.identity.v1.SignupService/GetSetupAvailability`                  | ConnectRPC   | Check initial admin setup availability          |
| POST     | `/rpc/tango.identity.v1.SignupService/SetupInitialAdmin`                     | ConnectRPC   | Sign up initial admin user                      |
| POST     | `/rpc/tango.identity.v1.SignupService/ListSignupTokens`                      | ConnectRPC   | List signup tokens                              |
| POST     | `/rpc/tango.identity.v1.SignupService/CreateSignupToken`                     | ConnectRPC   | Create signup token                             |
| POST     | `/rpc/tango.identity.v1.SignupService/DeleteSignupToken`                     | ConnectRPC   | Delete signup token                             |
| POST     | `/rpc/tango.identity.v1.UserService/ListUsers`                               | ConnectRPC   | List users                                      |
| POST     | `/rpc/tango.identity.v1.UserService/CreateUser`                              | ConnectRPC   | Create user                                     |
| POST     | `/rpc/tango.identity.v1.UserService/GetUser`                                 | ConnectRPC   | Get user by ID                                  |
| POST     | `/rpc/tango.identity.v1.UserService/UpdateUser`                              | ConnectRPC   | Update user                                     |
| POST     | `/rpc/tango.identity.v1.UserService/DeleteUser`                              | ConnectRPC   | Delete user                                     |
| POST     | `/rpc/tango.identity.v1.UserService/UpdateMe`                                | ConnectRPC   | Update current user                             |
| POST     | `/rpc/tango.identity.v1.UserService/UpdateMyProfilePicture`                  | ConnectRPC   | Update current user's profile picture           |
| POST     | `/rpc/tango.identity.v1.UserService/DeleteMyProfilePicture`                  | ConnectRPC   | Reset current user's profile picture            |
| POST     | `/rpc/tango.identity.v1.UserService/UpdateProfilePicture`                    | ConnectRPC   | Update user profile picture                     |
| POST     | `/rpc/tango.identity.v1.UserService/DeleteProfilePicture`                    | ConnectRPC   | Reset user profile picture                      |
| POST     | `/rpc/tango.identity.v1.UserService/ListUserGroups`                          | ConnectRPC   | Get user groups                                 |
| POST     | `/rpc/tango.identity.v1.UserService/ReplaceUserGroups`                       | ConnectRPC   | Update user groups                              |
| POST     | `/rpc/tango.identity.v1.UserService/ListWebAuthnCredentials`                 | ConnectRPC   | List user passkeys                              |
| POST     | `/rpc/tango.identity.v1.UserService/UpdateWebAuthnCredential`                | ConnectRPC   | Rename user passkey                             |
| POST     | `/rpc/tango.identity.v1.UserService/DeleteWebAuthnCredential`                | ConnectRPC   | Delete user passkey                             |
| POST     | `ImpersonateUser` (procedure name TBD)                                       | ConnectRPC   | Impersonate a user (admin) — **TODO(impersonation)**, not implemented |
| POST     | `/rpc/tango.identity.v1.OneTimeAccessService/RequestEmail`                   | ConnectRPC   | Request one-time access email                   |
| POST     | `/rpc/tango.identity.v1.OneTimeAccessService/AdminSendEmail`                 | ConnectRPC   | Request one-time access email (admin)           |
| POST     | `/rpc/tango.identity.v1.OneTimeAccessService/AdminIssueToken`                | ConnectRPC   | Create one-time access token for user (admin)   |
| POST     | `/api/one-time-access-token/{token}`                                         | HTTP/REST    | Exchange one-time access token                  |
| POST     | `/rpc/tango.identity.v1.EmailVerificationService/SendEmail`                  | ConnectRPC   | Send email verification                         |
| POST     | `/api/users/me/verify-email`                                                 | HTTP/REST    | Verify email                                    |
| GET      | `/api/users/{id}/profile-picture.png`                                        | HTTP/REST    | Get user profile picture                        |

## User Groups

A user group is an organizing unit: it carries no permission of its own, but the members it
gathers are addressed together — a later feature may hang claims on a group or gate a client on
it, and every member inherits the outcome. Upstream attaches custom claims to groups and gates
OIDC clients on them; tango's group is the membership alone until those features land, which is
why the surface holds the six procedures below and nothing about claims or clients.

The `name` is the group's unique handle — the unique index is the storage of that rule, and a
duplicate answers `already_exists`. The `display_name` is what the UI shows. The member count in
every answer is what the query computes, never a column, so a group with no members answers zero
because the join is left.

The membership update is a replace, the way the upstream endpoint it ports is: the request names
the whole member set, and an empty list empties the group. Every identifier must name an account
— a member that does not exist would make the group wrong rather than merely empty, so the
replacement is refused whole and the group keeps the set it held.

| Method   | Procedure / Endpoint                                                             | Protocol     | Summary                           |
| -------- | -------------------------------------------------------------------------------- | ------------ | --------------------------------- |
| POST     | `/rpc/tango.identity.v1.UserGroupService/ListUserGroups`                          | ConnectRPC   | List user groups                  |
| POST     | `/rpc/tango.identity.v1.UserGroupService/GetUserGroup`                            | ConnectRPC   | Get user group by ID              |
| POST     | `/rpc/tango.identity.v1.UserGroupService/CreateUserGroup`                         | ConnectRPC   | Create user group                 |
| POST     | `/rpc/tango.identity.v1.UserGroupService/UpdateUserGroup`                         | ConnectRPC   | Update user group                 |
| POST     | `/rpc/tango.identity.v1.UserGroupService/DeleteUserGroup`                         | ConnectRPC   | Delete user group                 |
| POST     | `/rpc/tango.identity.v1.UserGroupService/SetUserGroupMembers`                     | ConnectRPC   | Update users in a group           |

## Well Known

Discovery endpoints for OpenID Connect.

| Method   | Service / Endpoint                          | Protocol    | Summary                                       |
| -------- | ------------------------------------------- | ----------- | --------------------------------------------- |
| GET      | `/.well-known/openid-configuration`         | HTTP/REST   | Get OpenID Connect discovery configuration    |
| GET      | `/.well-known/oauth-authorization-server`   | HTTP/REST   | Get OAuth 2.0 authorization server metadata   |
| GET      | `/.well-known/jwks.json`                    | HTTP/REST   | Get JSON Web Key Set (JWKS)                   |

## APIs

Resource APIs, permissions, and client grants.

| Method   | Procedure / Endpoint                                                       | Protocol     | Summary                                         |
| -------- | -------------------------------------------------------------------------- | ------------ | ----------------------------------------------- |
| POST     | `/rpc/tango.admin.v1.ApiService/ListApis`                                  | ConnectRPC   | List APIs                                       |
| POST     | `/rpc/tango.admin.v1.ApiService/CreateAPI`                                 | ConnectRPC   | Create API                                      |
| POST     | `/rpc/tango.admin.v1.ApiService/GetAPI`                                    | ConnectRPC   | Get API by ID                                   |
| POST     | `/rpc/tango.admin.v1.ApiService/UpdateAPI`                                 | ConnectRPC   | Update API                                      |
| POST     | `/rpc/tango.admin.v1.ApiService/DeleteAPI`                                 | ConnectRPC   | Delete API                                      |
| POST     | `/rpc/tango.admin.v1.ApiService/SetPermissions`                            | ConnectRPC   | Update API permissions                          |
| POST     | `/rpc/tango.admin.v1.ApiService/SetCimdAccess`                             | ConnectRPC   | Update metadata document client access          |
| POST     | `/rpc/tango.admin.v1.ApiService/ListAssignableClients`                     | ConnectRPC   | List clients that can still be granted access   |
| POST     | `/rpc/tango.admin.v1.ApiService/ListClients`                               | ConnectRPC   | List clients with access to an API              |
| POST     | `/rpc/tango.admin.v1.ApiService/GrantClient`                               | ConnectRPC   | Grant a client access to an API                 |
| POST     | `/rpc/tango.admin.v1.ApiService/RevokeClient`                              | ConnectRPC   | Revoke a client's access to an API              |
| POST     | `/rpc/tango.admin.v1.ApiService/ListApisForClient`                         | ConnectRPC   | List APIs a client may access                   |
| POST     | `/rpc/tango.admin.v1.ApiService/ListAssignableApisForClient`               | ConnectRPC   | List APIs a client can still be granted         |

## Device Login

The device side is an integration flow and stays on HTTP; the approval UI is first-party and moved
to ConnectRPC.

| Method   | Procedure / Endpoint                                                         | Protocol     | Summary                         |
| -------- | ---------------------------------------------------------------------------- | ------------ | ------------------------------- |
| POST     | `/api/device-login/requests`                                                 | HTTP/REST    | Create device login request     |
| POST     | `/api/device-login/requests/{id}/exchange`                                   | HTTP/REST    | Exchange device login request   |
| POST     | `/rpc/tango.identity.v1.DeviceApprovalService/GetPendingRequest`             | ConnectRPC   | Inspect device login request    |
| POST     | `/rpc/tango.identity.v1.DeviceApprovalService/DecideRequest`                 | ConnectRPC   | Decide device login request     |

## SCIM

SCIM service-provider endpoints: CRUD plus the sync push.

| Method   | Procedure / Endpoint                                               | Protocol     | Summary                         |
| -------- | ------------------------------------------------------------------ | ------------ | ------------------------------- |
| POST     | `/rpc/tango.federation.v1.ScimProviderService/Upsert`              | ConnectRPC   | Create SCIM service provider    |
| POST     | `/rpc/tango.federation.v1.ScimProviderService/Update`              | ConnectRPC   | Update SCIM service provider    |
| POST     | `/rpc/tango.federation.v1.ScimProviderService/Delete`              | ConnectRPC   | Delete SCIM service provider    |
| POST     | `/rpc/tango.federation.v1.ScimProviderService/Sync`                | ConnectRPC   | Sync SCIM service provider      |

## Webhooks

Tango-only outbound webhooks. Every delivery is signed with HMAC-SHA256 and retried on the queue.

| Method   | Procedure / Endpoint                                                | Protocol     | Summary                           |
| -------- | ------------------------------------------------------------------- | ------------ | --------------------------------- |
| POST     | `/rpc/tango.webhook.v1.WebhookService/List`                         | ConnectRPC   | List webhook endpoints            |
| POST     | `/rpc/tango.webhook.v1.WebhookService/Create`                       | ConnectRPC   | Create a webhook endpoint         |
| POST     | `/rpc/tango.webhook.v1.WebhookService/Get`                          | ConnectRPC   | Get a webhook endpoint            |
| POST     | `/rpc/tango.webhook.v1.WebhookService/Update`                       | ConnectRPC   | Update a webhook endpoint         |
| POST     | `/rpc/tango.webhook.v1.WebhookService/Delete`                       | ConnectRPC   | Delete a webhook endpoint         |
| POST     | `/rpc/tango.webhook.v1.WebhookService/RotateSecret`                 | ConnectRPC   | Rotate the signing secret         |
| POST     | `/rpc/tango.webhook.v1.WebhookService/Test`                         | ConnectRPC   | Send a test delivery              |
| POST     | `/rpc/tango.webhook.v1.WebhookService/ListDeliveries`               | ConnectRPC   | List deliveries of one endpoint   |
| POST     | `/rpc/tango.webhook.v1.WebhookService/ListAllDeliveries`            | ConnectRPC   | List all deliveries               |

## Version

Version metadata endpoints.

| Method   | Procedure / Endpoint                                     | Protocol     | Summary                        |
| -------- | -------------------------------------------------------- | ------------ | ------------------------------ |
| POST     | `/rpc/tango.system.v1.VersionService/Current`            | ConnectRPC   | Get current deployed version   |
| POST     | `/rpc/tango.system.v1.VersionService/Latest`             | ConnectRPC   | Get latest available version   |

## Health Check

Healthcheck endpoints plus the transport smoke target.

| Method   | Procedure / Endpoint                               | Protocol     | Summary                    |
| -------- | -------------------------------------------------- | ------------ | -------------------------- |
| GET      | `/healthz`                                         | HTTP/REST    | Liveness; touches no dependency |
| GET      | `/api/healthz`                                     | HTTP/REST    | Readiness document         |
| POST     | `/rpc/tango.system.v1.HealthService/Check`         | ConnectRPC   | Connect transport smoke    |

## Infrastructure

Non-API routes the server mounts. They serve the deployment and the SPA, not the application
contract.

| Method   | Endpoint                | Protocol   | Summary                                    |
| -------- | ----------------------- | ---------- | ------------------------------------------ |
| GET      | `/`                     | HTTP/REST  | SPA document; every unmatched path falls back to it |
| GET      | `/api/`                 | HTTP/REST  | API root document (name, version, platform) |
| GET      | `/.well-known/version`  | HTTP/REST  | Bare version document for tooling          |
| GET      | `/static/*`             | HTTP/REST  | Embedded static assets                     |
