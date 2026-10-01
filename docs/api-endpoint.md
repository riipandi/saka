# API Endpoints

The surfaces the server answers, reconciled against the code on 2026-09-30. The agent-facing
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

| Method   | Procedure / Endpoint                                          | Protocol     | Summary                             |
| -------- | ------------------------------------------------------------- | ------------ | ----------------------------------- |
| POST     | `/rpc/tango.authn.v1.AuthService/SignIn`                      | ConnectRPC   | Sign in with password               |
| POST     | `/rpc/tango.authn.v1.SessionService/Refresh`                  | ConnectRPC   | Refresh the token pair (rotates the refresh token) |
| POST     | `/rpc/tango.authn.v1.SessionService/GetSession`               | ConnectRPC   | Inspect current session             |
| POST     | `/rpc/tango.authn.v1.SessionService/ListSessions`             | ConnectRPC   | List own sessions                   |
| POST     | `/rpc/tango.authn.v1.SessionService/RevokeSession`            | ConnectRPC   | Revoke one own session              |
| POST     | `/rpc/tango.authn.v1.SessionService/SignOut`                  | ConnectRPC   | Sign out                            |
| POST     | `/rpc/tango.authn.v1.SessionService/SignOutOtherSessions`     | ConnectRPC   | Sign out other sessions             |
| POST     | `/rpc/tango.authn.v1.SessionService/SignOutAllSessions`       | ConnectRPC   | Sign out all sessions               |
| POST     | `/rpc/tango.authn.v1.SessionService/ImpersonateUser`          | ConnectRPC   | Impersonate a user (admin)          |
| POST     | `/rpc/tango.authn.v1.SessionService/StopImpersonating`        | ConnectRPC   | Stop impersonating                  |

## Password Recovery (tango-only)

| Method   | Procedure / Endpoint                                         | Protocol     | Summary                             |
| -------- | ------------------------------------------------------------ | ------------ | ----------------------------------- |
| POST     | `/rpc/tango.authn.v1.PasswordRecoveryService/ForgotPassword` | ConnectRPC   | Request a password reset (anti-enumeration) |
| POST     | `/rpc/tango.authn.v1.PasswordRecoveryService/ResetPassword`  | ConnectRPC   | Reset with the emailed token        |
| POST     | `/rpc/tango.authn.v1.PasswordRecoveryService/AdminResetUserPassword` | ConnectRPC | Trigger a reset for one account (admin) |

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
| POST     | `/rpc/tango.authn.v1.OneTimeAccessService/CreateToken`                      | ConnectRPC   | Create one-time access token for user (admin) |
| POST     | `/rpc/tango.authn.v1.OneTimeAccessService/ExchangeToken`                    | ConnectRPC   | Exchange one-time access token               |
| POST     | `/rpc/tango.authn.v1.OneTimeAccessService/RequestEmailAsAdmin`              | ConnectRPC   | Request one-time access email (admin)        |
| POST     | `/rpc/tango.authn.v1.OneTimeAccessService/RequestEmail`                     | ConnectRPC   | Request one-time access email                |

## Passkeys (WebAuthn)

The passwordless surface: registration and ceremony sign-in ride the WebAuthn protocol; the
administrative rows manage credentials over a named account. Step-up reauthentication mints a
single-use token that lives `session.reverification_window` (default 30 minutes).

| Method   | Procedure / Endpoint                                                        | Protocol     | Summary                                     |
| -------- | --------------------------------------------------------------------------- | ------------ | ------------------------------------------- |
| POST     | `/rpc/tango.authn.v1.WebAuthnService/BeginRegistration`                     | ConnectRPC   | Open a credential-registration ceremony (session) |
| POST     | `/rpc/tango.authn.v1.WebAuthnService/VerifyRegistration`                    | ConnectRPC   | Verify the attestation and store the credential |
| POST     | `/rpc/tango.authn.v1.WebAuthnService/ListCredentials`                       | ConnectRPC   | List the account's passkeys                 |
| POST     | `/rpc/tango.authn.v1.WebAuthnService/UpdateCredential`                      | ConnectRPC   | Rename a passkey                            |
| POST     | `/rpc/tango.authn.v1.WebAuthnService/DeleteCredential`                      | ConnectRPC   | Remove a passkey (second-factor proof)      |
| POST     | `/rpc/tango.authn.v1.WebAuthnService/BeginLogin`                            | ConnectRPC   | Open a ceremony sign-in challenge           |
| POST     | `/rpc/tango.authn.v1.WebAuthnService/VerifyLogin`                           | ConnectRPC   | Verify the assertion and open the session   |
| POST     | `/rpc/tango.authn.v1.WebAuthnService/Reauthenticate`                        | ConnectRPC   | Step-up proof (password or passkey)         |
| POST     | `/rpc/tango.authn.v1.WebAuthnService/AdminListCredentials`                  | ConnectRPC   | List a user's passkeys (admin)              |
| POST     | `/rpc/tango.authn.v1.WebAuthnService/AdminUpdateCredential`                 | ConnectRPC   | Rename a user's passkey (admin)             |
| POST     | `/rpc/tango.authn.v1.WebAuthnService/AdminDeleteCredential`                 | ConnectRPC   | Remove a user's passkey (admin)             |

## MFA TOTP

Tango-only; upstream Pocket ID has no TOTP. A confirmed enrollment turns a successful password
sign-in into a pending bridge (5-minute TTL, 3-wrong-codes budget) that only `CompleteSignIn`
completes — with a TOTP code or a recovery code.

| Method   | Procedure / Endpoint                                          | Protocol     | Summary                      |
| -------- | ------------------------------------------------------------- | ------------ | ---------------------------- |
| POST     | `/rpc/tango.authn.v1.MultifactorService/BeginTotpEnrollment`  | ConnectRPC   | Start TOTP enrollment (secret shown once, sealed at rest) |
| POST     | `/rpc/tango.authn.v1.MultifactorService/ConfirmTotpEnrollment`| ConnectRPC   | Confirm and enable TOTP (recovery codes shown once) |
| POST     | `/rpc/tango.authn.v1.MultifactorService/ListTotpEnrollments`  | ConnectRPC   | List the account's authenticators (never a secret) |
| POST     | `/rpc/tango.authn.v1.MultifactorService/DeleteTotpEnrollment` | ConnectRPC   | Delete one authenticator (second-factor proof) |
| POST     | `/rpc/tango.authn.v1.MultifactorService/CompleteSignIn`       | ConnectRPC   | Complete a pending sign-in   |
| POST     | `/rpc/tango.authn.v1.MultifactorService/RegenerateRecoveryCodes` | ConnectRPC | Rotate recovery codes (shown once) |
| POST     | `/rpc/tango.authn.v1.MultifactorService/DisableMfa`           | ConnectRPC   | Disable MFA (second-factor proof) |
| POST     | `/rpc/tango.authn.v1.MultifactorService/VerifyRecoveryCode`   | ConnectRPC   | Spend one recovery code as a standalone proof |
| POST     | `/rpc/tango.authn.v1.MultifactorService/AdminDisableMfa`      | ConnectRPC   | Disable a user's MFA (admin, no proof needed) |

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

## Authorization

Permissions are code-declared `resource:id:action`; a role is a named set; grants are the
account's roles plus its direct grants. The catalog is seeded and read-only.

| Method   | Procedure / Endpoint                                                        | Protocol     | Summary          |
| -------- | --------------------------------------------------------------------------- | ------------ | ---------------- |
| POST     | `/rpc/tango.authz.v1.AuthorizationService/ListPermissions`                   | ConnectRPC   | List the permission catalog |
| POST     | `/rpc/tango.authz.v1.AuthorizationService/ListRoles`                         | ConnectRPC   | List roles       |
| POST     | `/rpc/tango.authz.v1.AuthorizationService/GetRole`                           | ConnectRPC   | Get role by ID   |
| POST     | `/rpc/tango.authz.v1.AuthorizationService/CreateRole`                        | ConnectRPC   | Create role      |
| POST     | `/rpc/tango.authz.v1.AuthorizationService/UpdateRole`                        | ConnectRPC   | Update role      |
| POST     | `/rpc/tango.authz.v1.AuthorizationService/DeleteRole`                        | ConnectRPC   | Delete role      |
| POST     | `/rpc/tango.authz.v1.AuthorizationService/SetRolePermissions`                | ConnectRPC   | Replace a role's permissions |
| POST     | `/rpc/tango.authz.v1.AuthorizationService/ListUserRoles`                     | ConnectRPC   | List an account's roles |
| POST     | `/rpc/tango.authz.v1.AuthorizationService/SetUserRoles`                      | ConnectRPC   | Replace an account's roles |
| POST     | `/rpc/tango.authz.v1.AuthorizationService/ListUserPermissions`               | ConnectRPC   | List an account's direct grants |
| POST     | `/rpc/tango.authz.v1.AuthorizationService/SetUserPermissions`                | ConnectRPC   | Replace an account's direct grants |

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
| POST     | `/rpc/tango.identity.v1.UserService/UpdateCurrentUser`                       | ConnectRPC   | Update the signed-in account's own profile (names, locale, timezone) |
| POST     | `/rpc/tango.identity.v1.UserService/ListUsers`                               | ConnectRPC   | List users (admin)                              |
| POST     | `/rpc/tango.identity.v1.UserService/GetUser`                                 | ConnectRPC   | Get user by ID (admin)                          |
| POST     | `/rpc/tango.identity.v1.UserService/CreateUser`                              | ConnectRPC   | Create user (admin; optional `user_group_ids` joins groups at creation) |
| POST     | `/rpc/tango.identity.v1.UserService/UpdateUser`                              | ConnectRPC   | Update user (admin)                             |
| POST     | `/rpc/tango.identity.v1.UserService/DeleteUser`                              | ConnectRPC   | Delete user (admin; refuses the signed-in account) |
| POST     | `/rpc/tango.identity.v1.UserService/BanUser`                                 | ConnectRPC   | Ban a user (admin)                              |
| POST     | `/rpc/tango.identity.v1.UserService/UnbanUser`                               | ConnectRPC   | Unban a user (admin)                            |
| POST     | `/rpc/tango.identity.v1.UserService/ResetProfilePicture`                     | ConnectRPC   | Reset user profile picture                      |
| POST     | `/rpc/tango.identity.v1.UserService/DeleteMyAccount`                         | ConnectRPC   | Delete the signed-in account (self-service; gated by `users.self_delete_enabled` + per-account override; soft-deleted into `deleted_records`) |
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
| POST     | `/rpc/tango.identity.v1.EmailVerificationService/RequestEmailChange`         | ConnectRPC   | Request email change (confirm code to the new address) |
| POST     | `/rpc/tango.identity.v1.EmailVerificationService/ConfirmEmailChange`         | ConnectRPC   | Confirm email change (token is the credential)   |

## Notifications

Audiences `global`, `users`, `user_groups` — resolved by query at read time, never fanned out at
create. Read state is one receipt per account. `WatchNotifications` is the in-process broker's
stream; `system` notices target accounts and carry neither a topic nor a global audience.

| Method   | Procedure / Endpoint                                                         | Protocol     | Summary                                         |
| -------- | ---------------------------------------------------------------------------- | ------------ | ----------------------------------------------- |
| POST     | `/rpc/tango.notification.v1.NotificationService/CreateNotification`          | ConnectRPC   | Create a notification (admin)                   |
| POST     | `/rpc/tango.notification.v1.NotificationService/GetNotification`             | ConnectRPC   | Get one notification                            |
| POST     | `/rpc/tango.notification.v1.NotificationService/ListAllNotifications`        | ConnectRPC   | List every notification (admin)                 |
| POST     | `/rpc/tango.notification.v1.NotificationService/CancelNotification`          | ConnectRPC   | Cancel one notification (admin)                 |
| POST     | `/rpc/tango.notification.v1.NotificationService/ListNotifications`           | ConnectRPC   | List the caller's notifications                 |
| POST     | `/rpc/tango.notification.v1.NotificationService/MarkNotificationRead`        | ConnectRPC   | Mark one read                                   |
| POST     | `/rpc/tango.notification.v1.NotificationService/MarkAllNotificationsRead`    | ConnectRPC   | Mark every notification read                    |
| POST     | `/rpc/tango.notification.v1.NotificationService/UnreadCount`                 | ConnectRPC   | Count unread notifications                      |
| POST     | `/rpc/tango.notification.v1.NotificationService/WatchNotifications`          | ConnectRPC   | Stream the broker (server-streaming)            |

## Settings

Tango-only. The catalog in code declares every item; `public.settings` stores the overrides
alone. A sealed item seals its value (AES-256-GCM); a public item never rests sealed.

| Method   | Procedure / Endpoint                                                         | Protocol     | Summary                                         |
| -------- | ---------------------------------------------------------------------------- | ------------ | ----------------------------------------------- |
| POST     | `/rpc/tango.settings.v1.SettingsService/List`                                | ConnectRPC   | List settings with effective values (admin)     |
| POST     | `/rpc/tango.settings.v1.SettingsService/Update`                              | ConnectRPC   | Update one setting (admin)                      |
| POST     | `/rpc/tango.settings.v1.SettingsService/Reset`                               | ConnectRPC   | Reset one setting to its default (admin)        |
| POST     | `/rpc/tango.settings.v1.SettingsService/ListPublic`                          | ConnectRPC   | List public settings (anonymous)                |

## Application Configuration

| Method   | Procedure / Endpoint                                                         | Protocol     | Summary                                         |
| -------- | ---------------------------------------------------------------------------- | ------------ | ----------------------------------------------- |
| GET      | `/api/configuration`                                                         | HTTP/REST    | The configuration document: the public subset to an anonymous caller, every non-secret setting to an administrator |
| POST     | `/rpc/tango.system.v1.AppConfigService/TestEmail`                            | ConnectRPC   | Send a test email (admin)                       |

The configuration's source is the JSON file resolved at startup; there is no write surface.

## OIDC Federation

The provider surface: client management and consent over ConnectRPC, the protocol itself as REST
under `/oidc` (the specifications' own shapes). Discovery at
`/.well-known/openid-configuration`; JWKS at `/.well-known/jwks.json`.

| Method   | Service / Endpoint                                                            | Protocol     | Summary                                     |
| -------- | ----------------------------------------------------------------------------- | ------------ | ------------------------------------------- |
| POST     | `/rpc/tango.federation.v1.OidcClientService/ListClients`                      | ConnectRPC   | List OIDC clients (admin)                   |
| POST     | `/rpc/tango.federation.v1.OidcClientService/CreateClient`                     | ConnectRPC   | Create OIDC client (admin; first secret shown once) |
| POST     | `/rpc/tango.federation.v1.OidcClientService/GetClient`                        | ConnectRPC   | Get OIDC client (admin)                     |
| POST     | `/rpc/tango.federation.v1.OidcClientService/UpdateClient`                     | ConnectRPC   | Update OIDC client (admin)                  |
| POST     | `/rpc/tango.federation.v1.OidcClientService/DeleteClient`                     | ConnectRPC   | Delete OIDC client (admin)                  |
| POST     | `/rpc/tango.federation.v1.OidcClientService/UpdateAllowedUserGroups`          | ConnectRPC   | Replace a client's allowed groups (admin)   |
| POST     | `/rpc/tango.federation.v1.OidcClientService/GetClientMeta`                    | ConnectRPC   | Get client metadata (admin)                 |
| POST     | `/rpc/tango.federation.v1.OidcClientService/PreviewClient`                    | ConnectRPC   | Preview the claims a client would receive (admin) |
| POST     | `/rpc/tango.federation.v1.OidcClientService/UploadLogo`                       | ConnectRPC   | Upload client logo (admin)                  |
| POST     | `/rpc/tango.federation.v1.OidcClientService/DeleteLogo`                       | ConnectRPC   | Delete client logo (admin)                  |
| POST     | `/rpc/tango.federation.v1.OidcClientService/ListSecrets`                      | ConnectRPC   | List client secrets (admin)                 |
| POST     | `/rpc/tango.federation.v1.OidcClientService/CreateSecret`                     | ConnectRPC   | Create client secret (admin; shown once)    |
| POST     | `/rpc/tango.federation.v1.OidcClientService/DeleteSecret`                     | ConnectRPC   | Delete client secret (admin)                |
| POST     | `/rpc/tango.federation.v1.OidcClientService/RefreshClient`                    | ConnectRPC   | Re-fetch a CIMD client's document (admin)   |
| POST     | `/rpc/tango.federation.v1.OidcConsentService/ListMyAuthorizedClients`         | ConnectRPC   | The caller's consent ledger                 |
| POST     | `/rpc/tango.federation.v1.OidcConsentService/RevokeMyAuthorizedClient`        | ConnectRPC   | Revoke the caller's consent for one client  |
| POST     | `/rpc/tango.federation.v1.OidcConsentService/ListMyClients`                   | ConnectRPC   | The clients the caller may authorize        |
| POST     | `/rpc/tango.federation.v1.OidcConsentService/ListUserAuthorizedClients`       | ConnectRPC   | One account's consent ledger (admin)        |
| POST     | `/rpc/tango.federation.v1.OidcConsentService/ListAllAuthorizedClients`        | ConnectRPC   | The deployment-wide ledger (admin)          |
| POST     | `/rpc/tango.federation.v1.CustomClaimService/Suggest`                         | ConnectRPC   | Custom-claim key suggestions (admin)        |
| POST     | `/rpc/tango.federation.v1.CustomClaimService/List{User,Group}Claims`          | ConnectRPC   | List a subject's claims (admin)             |
| POST     | `/rpc/tango.federation.v1.CustomClaimService/Create{User,Group}Claim`         | ConnectRPC   | Create a claim on a subject (admin)         |
| POST     | `/rpc/tango.federation.v1.CustomClaimService/Update{User,Group}Claim`         | ConnectRPC   | Replace one claim row (admin)               |
| POST     | `/rpc/tango.federation.v1.CustomClaimService/Delete{User,Group}Claim`         | ConnectRPC   | Delete one claim row (admin)                |
| POST     | `/rpc/tango.identity.v1.UserGroupService/SetAllowedOidcClients`               | ConnectRPC   | Replace a group's client allowlist (admin)  |
| GET, POST | `/oidc/authorize`                                                            | HTTP/REST    | Authorization endpoint                      |
| POST     | `/oidc/token`                                                                 | HTTP/REST    | Token endpoint (code, refresh, device grants) |
| GET, POST | `/oidc/userinfo`                                                             | HTTP/REST    | Userinfo (bearer)                           |
| POST     | `/oidc/introspect`                                                            | HTTP/REST    | Introspection (client-scoped, RFC 7662)     |
| POST     | `/oidc/par`                                                                   | HTTP/REST    | Pushed authorization request (RFC 9126)     |
| POST     | `/oidc/device_authorization`                                                  | HTTP/REST    | Device authorization grant (RFC 8628)       |
| GET, POST | `/oidc/device[/{callback}]`                                                  | HTTP/REST    | Device verification (browser)               |
| GET, POST | `/oidc/end-session`                                                          | HTTP/REST    | RP-initiated logout                         |
| GET      | `/oidc/clients/{id}/logo`                                                     | HTTP/REST    | Client logo (public)                        |

## SCIM Provisioning

Outbound provisioning: tango is the SCIM client, not the server. One provider row per OIDC
client names a remote base URL and the bearer token the sync presents (sealed at rest, shown
once at create). One pass pushes the client's visible accounts and groups out until the remote
matches the local snapshot; it also runs hourly and, debounced, after account or group changes.

| Method   | Service / Endpoint                                            | Protocol     | Summary                                    |
| -------- | ------------------------------------------------------------- | ------------ | ------------------------------------------ |
| POST     | `/rpc/tango.federation.v1.ScimProviderService/GetByClient`    | ConnectRPC   | The provider one client syncs to (admin)   |
| POST     | `/rpc/tango.federation.v1.ScimProviderService/Create`         | ConnectRPC   | Attach a provisioning target (admin; token shown once) |
| POST     | `/rpc/tango.federation.v1.ScimProviderService/Update`         | ConnectRPC   | Replace endpoint and token (admin; empty token keeps the stored one) |
| POST     | `/rpc/tango.federation.v1.ScimProviderService/Delete`         | ConnectRPC   | Remove the provisioning target (admin)     |
| POST     | `/rpc/tango.federation.v1.ScimProviderService/Sync`           | ConnectRPC   | Run one provisioning pass now (admin; counts in the answer) |

## Webhooks (tango-only)

The outbound event surface: an administrator registers a destination — a URL, a method, optional
custom headers — and subscribes it to the event catalog. Every audit record is a candidate
delivery, mapped onto the dot-named catalog (`user.created`, `session.signed_in`, …); an endpoint
that lists no events, or lists the `*` wildcard, receives every one of them. Deliveries are
HMAC-SHA256 signed (`t=<unix>,v1=<hex>` over timestamp + exact body bytes) and retried five
times at a thirty-second backoff; a redirect, a 4xx other than 408/429, a disabled endpoint, or
a host the destination policy refuses fails the delivery on its first answer. Deliveries never
follow a redirect, so the signature headers never reach a redirect target. By default a
destination whose host resolves to a loopback, private, or link-local address is refused — the
SSRF guard; `webhook.allow_private_network` lifts it for deployments whose receivers live beside
the server. The attempt rows carry redacted response metadata only, attempt rows age out at
seven days and terminal deliveries at thirty, and the signing secret is sealed at rest and shown
exactly once.

| Method   | Procedure / Endpoint                                          | Protocol     | Summary                                          |
| -------- | ------------------------------------------------------------- | ------------ | ------------------------------------------------ |
| POST     | `/rpc/tango.webhook.v1.WebhookService/List`                   | ConnectRPC   | List endpoints (admin)                           |
| POST     | `/rpc/tango.webhook.v1.WebhookService/Create`                 | ConnectRPC   | Register an endpoint (secret shown once) (admin) |
| POST     | `/rpc/tango.webhook.v1.WebhookService/Get`                    | ConnectRPC   | Read one endpoint (admin)                        |
| POST     | `/rpc/tango.webhook.v1.WebhookService/Update`                 | ConnectRPC   | Rewrite an endpoint (admin)                      |
| POST     | `/rpc/tango.webhook.v1.WebhookService/Delete`                 | ConnectRPC   | Remove an endpoint (admin)                       |
| POST     | `/rpc/tango.webhook.v1.WebhookService/RotateSecret`           | ConnectRPC   | Replace the signing secret (admin)               |
| POST     | `/rpc/tango.webhook.v1.WebhookService/Test`                   | ConnectRPC   | Queue a `webhook.test` delivery (admin)          |
| POST     | `/rpc/tango.webhook.v1.WebhookService/ListDeliveries`         | ConnectRPC   | List one endpoint's deliveries (admin)           |
| POST     | `/rpc/tango.webhook.v1.WebhookService/ListAllDeliveries`      | ConnectRPC   | List every delivery (admin)                      |
| POST     | `/rpc/tango.webhook.v1.WebhookService/ListEventTypes`         | ConnectRPC   | The event catalog (admin)                        |

## Device Login

The passkey-less pairing sign-in: a browser that cannot sign itself in creates a request, another
browser approves it, and the creating browser's long poll answers. The user code is
`XXXX-XXXX` over an alphabet without ambiguous characters; the code and the pairing secret live
only as SHA-256 hashes; decisions are single-use.

| Method   | Service / Endpoint                                            | Protocol     | Summary                                    |
| -------- | ------------------------------------------------------------- | ------------ | ------------------------------------------ |
| POST     | `/api/device-login/requests`                                  | HTTP/REST    | Create a pairing request (pairing cookie rides the response) |
| POST     | `/api/device-login/requests/{id}/exchange`                    | HTTP/REST    | Long-poll the decision (25s window; 202 while pending) |
| POST     | `/rpc/tango.authn.v1.DeviceApprovalService/Inspect`           | ConnectRPC   | Read the request the code names (authenticated) |
| POST     | `/rpc/tango.authn.v1.DeviceApprovalService/Decide`            | ConnectRPC   | Approve or deny (authenticated; single decision) |

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
| GET      | `/.well-known/openid-configuration`         | HTTP/REST   | OpenID Connect discovery (when `oidc.enabled`) |

## Infrastructure

Non-API routes the server mounts. They serve the deployment and the SPA, not the application
contract.

| Method   | Endpoint                | Protocol   | Summary                                    |
| -------- | ----------------------- | ---------- | ------------------------------------------ |
| GET      | `/`                     | HTTP/REST  | SPA document; every unmatched path falls back to it |
| GET      | `/api/`                 | HTTP/REST  | API root document (name, version, platform) |
| GET      | `/static/*`             | HTTP/REST  | Embedded static assets, plus served uploads |
| GET      | `/api/uploads/{key}`    | HTTP/REST  | Upload progress (authenticated): the manifest's `status` and `size` for one storage key |

Debug-build-only utilities (`/debug/do`, `/debug/encode-id`, `/debug/decode-id`) answer `404` in
a release build.

## Planned (not implemented)

The scaffolds below exist as empty packages waiting for their phase; **no proto, no routes** —
every row is uncallable. The designed surfaces live in `.llms/endpoint-reference.md`.

| Feature | Scaffold | Surfaces planned |
| ------- | -------- | ---------------- |
| API resources (upstream `ApiService`) | — | `tango.admin.v1.ApiService` |
| Version metadata | — | `tango.system.v1.VersionService` |
| Initial admin setup | — | `tango initialize` + `tango admin:reset-password` (CLI, release build); the RPC stubs `SignupService/GetSetupAvailability` and `SetupInitialAdmin` are excluded |

## Future Improvements (deferred, decided 2026-09-28)

**Idempotency keys for non-idempotent procedures** (`X-Idempotency-Key`). A surface audit found
almost every mutating procedure already safe by design — single-use tokens spent through CAS
`UPDATE`s, `UNIQUE` refusals on creations, set-semantics `Set*` procedures, one-minute resend
cooldowns on the email senders. The one procedure a retried `POST` really corrupts is
`NotificationService/CreateNotification` (a plain insert whose retry duplicates the notice and
re-runs the announcement email fan-out). If a non-SPA client with aggressive retries ever calls
this API, add a Connect interceptor on an opt-in whitelist (the email senders and the
administrative creations): the key is scoped per caller and procedure, a key reused with a
different payload is refused, the original response is replayed with an
`X-Idempotency-Replayed: true` header, and `SignIn`/`Refresh` are never replayed (a cached
response would be a stored credential). Interim mitigation: client-side dedup in the SPA and the
per-IP rate limit both surfaces already sit behind.
