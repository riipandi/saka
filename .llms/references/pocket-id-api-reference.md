# Pocket ID API Reference

Upstream contract for the surfaces tango ports. Extracted from
<https://pocket-id.org/swagger.yaml> (swagger 2.0, Pocket ID API v1.0) and
reconciled against the upstream release **v2.16.0** (2026-09-20; local clone at
`~/Developer/github.com/pocket-id/pocket-id`). The operation list matched the
release exactly; the shape deltas this reconciliation found are recorded in
`.llms/handover/handover-20260930_1337.md` and the per-area notes below.

This file is a reference only. tango serves ConnectRPC below `/rpc` for these
operations and keeps a small HTTP/REST surface for protocol endpoints; see
`docs/api-endpoint.md` for what tango actually mounts.

## Parity deltas (reconciled 2026-09-30, upstream v2.16.0)

The operation lists matched exactly. The shape deltas below are what a caller
porting from upstream will meet; each is a deliberate tango shape or a gap the
backlog owns, not a drift to fix silently.

- User views carry no `is_admin` (the role/permission system replaced it) and
  no `ldapId` (tango has no LDAP). `email` stays required **by decision
  (2026-09-30)**: tango signs in with passwords first, so the address is the
  recovery and notification channel every account must have — the nullable
  email upstream v2 carries is a passkey-first consequence tango does not
  share. Custom claims travel through `CustomClaimService` rather than
  riding the user views.
- `UserGroupDetail` answers members and `allowed_oidc_clients` — the
  allowlist roll the Set procedure wrote, read back where upstream's group
  view embeds it (fixed 2026-09-30).
- Signup tokens answer `raw_token` at create and never again; upstream's
  token list echoes the raw value. Tango's sign-up mints a password
  credential with the request; upstream's sign-up has no credential in it.
- One-time access is identifier-based for the administrative half
  (`id` + `ttl_seconds`), not upstream's email + duration.
- Device-login inspect answers `user_agent` where upstream answers device and
  geo (country, city); the exchange/decide shapes match.
- API keys, audit logs, OIDC clients (backchannel logout included), SCIM
  service providers, and application configuration are supersets of the
  upstream shapes; nothing upstream carries is missing from them.

Not ported, **by decision (2026-09-30)**: the upstream **APIs** entity and its
client-grant/CIMD surface — tango is a general boilerplate, not an auth
product, and the API-entity model has no tango counterpart. The Yaak
collection keeps the 13 upstream requests as reference, marked `not planned`.
Also not ported: LDAP and `/api/storage/sqlite-warning` (tango is
Postgres-only). Passkey support **is** planned, positioned GitHub-style but
with more flexibility (decisions 2026-09-30): password sign-in stays, and a
passkey may also serve as a passwordless **first factor**; the email stays
required so every account keeps its recovery path. One credential set serves
passwordless sign-in, second-factor completion after a password, and
step-up reauthentication. An assertion with user verification satisfies MFA
fully — no second factor is demanded after a passkey sign-in. Enrollment and
device limits come from the appconfig catalog (`mfa.max_enrollments`,
`passkey.max_credentials`), enforced at enrollment only.
`/api/version/*` is planned (`tango.system.v1.VersionService`).

## Operations

### API Keys

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| GET | `/api/api-keys` | List API keys | 200 |
| POST | `/api/api-keys` | Create API key | 201 |
| DELETE | `/api/api-keys/{id}` | Revoke API key | 204 |
| POST | `/api/api-keys/{id}/renew` | Renew API key | 200 |

### APIs

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| GET | `/api/api-access/{clientId}/apis` | List APIs a client may access | 200 |
| GET | `/api/api-access/{clientId}/assignable-apis` | List APIs a client can still be granted access to | 200 |
| GET | `/api/apis` | List APIs | 200 |
| POST | `/api/apis` | Create API | 201 |
| DELETE | `/api/apis/{id}` | Delete API | 204 |
| GET | `/api/apis/{id}` | Get API by ID | 200 |
| PUT | `/api/apis/{id}` | Update API | 200 |
| GET | `/api/apis/{id}/assignable-clients` | List clients that can still be granted access to an API | 200 |
| PUT | `/api/apis/{id}/cimd-access` | Update metadata document client access | 200 |
| GET | `/api/apis/{id}/clients` | List clients with access to an API | 200 |
| DELETE | `/api/apis/{id}/clients/{clientId}` | Revoke a client's access to an API | 204 |
| PUT | `/api/apis/{id}/clients/{clientId}` | Update a client's access to an API | 200 |
| PUT | `/api/apis/{id}/permissions` | Update API permissions | 200 |

### Application Configuration

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| GET | `/api/application-configuration` | List public application configurations | 200 |
| PUT | `/api/application-configuration` | Update application configurations | 200 |
| GET | `/api/application-configuration/all` | List all application configurations | 200 |
| POST | `/api/application-configuration/sync-ldap` | Synchronize LDAP | 204 |
| POST | `/api/application-configuration/test-email` | Send test email | 204 |

### Application Images

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| DELETE | `/api/application-images/background` | Delete background image | 204 |
| GET | `/api/application-images/background` | Get background image | 200 |
| PUT | `/api/application-images/background` | Update background image | 204 |
| DELETE | `/api/application-images/default-profile-picture` | Delete default profile picture image | 204 |
| GET | `/api/application-images/default-profile-picture` | Get default profile picture image | 200 |
| PUT | `/api/application-images/default-profile-picture` | Update default profile picture image | 204 |
| GET | `/api/application-images/email` | Get email logo image | 200 |
| PUT | `/api/application-images/email` | Update email logo | 204 |
| GET | `/api/application-images/favicon` | Get favicon | 200 |
| PUT | `/api/application-images/favicon` | Update favicon | 204 |
| DELETE | `/api/application-images/logo` | Delete logo image | 204 |
| GET | `/api/application-images/logo` | Get logo image | 200 |
| PUT | `/api/application-images/logo` | Update logo | 204 |

### Audit Logs

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| GET | `/api/audit-logs` | List audit logs | 200 |
| GET | `/api/audit-logs/all` | List all audit logs | 200 |
| GET | `/api/audit-logs/filters/client-names` | List client names | 200 |
| GET | `/api/audit-logs/filters/users` | List users with IDs | 200 |

### Custom Claims

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| GET | `/api/custom-claims/suggestions` | Get custom claim suggestions | 200 |
| PUT | `/api/custom-claims/user-group/{userGroupId}` | Update custom claims for a user group | 200 |
| PUT | `/api/custom-claims/user/{userId}` | Update custom claims for a user | 200 |

### Device Login

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| POST | `/api/device-login/requests` | Create device login request | 201 |
| POST | `/api/device-login/requests/{id}/exchange` | Exchange device login request | 200 |
| POST | `/api/device-login/verification` | Inspect device login request | 200 |
| POST | `/api/device-login/verification/decision` | Decide device login request | 204 |

### Health

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| GET | `/healthz` | Responds to healthchecks | 204 |

### OIDC

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| GET | `/api/oidc/clients` | List OIDC clients | 200 |
| POST | `/api/oidc/clients` | Create OIDC client | 201 |
| DELETE | `/api/oidc/clients/{id}` | Delete OIDC client | 204 |
| GET | `/api/oidc/clients/{id}` | Get OIDC client | 200 |
| PUT | `/api/oidc/clients/{id}` | Update OIDC client | 200 |
| PUT | `/api/oidc/clients/{id}/allowed-user-groups` | Update allowed user groups | 200 |
| DELETE | `/api/oidc/clients/{id}/logo` | Delete client logo | 204 |
| GET | `/api/oidc/clients/{id}/logo` | Get client logo | 200 |
| POST | `/api/oidc/clients/{id}/logo` | Update client logo | 204 |
| GET | `/api/oidc/clients/{id}/meta` | Get client metadata | 200 |
| GET | `/api/oidc/clients/{id}/preview/{userId}` | Preview OIDC client data for user | 200 |
| POST | `/api/oidc/clients/{id}/refresh` | Refresh client metadata document | 200 |
| GET | `/api/oidc/clients/{id}/scim-service-provider` | Get SCIM service provider | 200 |
| GET | `/api/oidc/clients/{id}/secrets` | List client secrets | 200 |
| POST | `/api/oidc/clients/{id}/secrets` | Create client secret | 201 |
| DELETE | `/api/oidc/clients/{id}/secrets/{secretId}` | Delete client secret | 204 |
| POST | `/api/oidc/introspect` | Introspect OIDC tokens | 200 |
| GET | `/api/oidc/userinfo` | Get user information | 200 |
| GET | `/api/oidc/users/me/authorized-clients` | List authorized clients for current user | 200 |
| DELETE | `/api/oidc/users/me/authorized-clients/{clientId}` | Revoke authorization for an OIDC client | 204 |
| GET | `/api/oidc/users/me/clients` | List accessible OIDC clients for current user | 200 |
| GET | `/api/oidc/users/{id}/authorized-clients` | List authorized clients for a user | 200 |
| PUT | `/api/user-groups/{id}/allowed-oidc-clients` | Update allowed OIDC clients | 200 |

### SCIM

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| POST | `/api/scim/service-provider` | Create SCIM service provider | 201 |
| DELETE | `/api/scim/service-provider/{id}` | Delete SCIM service provider | 204 |
| PUT | `/api/scim/service-provider/{id}` | Update SCIM service provider | 200 |
| POST | `/api/scim/service-provider/{id}/sync` | Sync SCIM service provider | 200 |

### Storage

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| GET | `/api/storage/sqlite-warning` | Get whether the SQLite storage warning should be shown | 200 |

### User Groups

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| GET | `/api/user-groups` | List user groups | 200 |
| POST | `/api/user-groups` | Create user group | 201 |
| DELETE | `/api/user-groups/{id}` | Delete user group | 204 |
| GET | `/api/user-groups/{id}` | Get user group by ID | 200 |
| PUT | `/api/user-groups/{id}` | Update user group | 200 |
| PUT | `/api/user-groups/{id}/users` | Update users in a group | 200 |

### Users

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| POST | `/api/one-time-access-email` | Request one-time access email | 204 |
| POST | `/api/one-time-access-token/{token}` | Exchange one-time access token | 200 |
| POST | `/api/signup` | Sign up | 201 |
| GET | `/api/signup-tokens` | List signup tokens | 200 |
| POST | `/api/signup-tokens` | Create signup token | 201 |
| DELETE | `/api/signup-tokens/{id}` | Delete signup token | 204 |
| POST | `/api/signup/setup` | Sign up initial admin user | 200 |
| GET | `/api/users` | List users | 200 |
| POST | `/api/users` | Create user | 201 |
| GET | `/api/users/me` | Get current user | 200 |
| PUT | `/api/users/me` | Update current user | 200 |
| DELETE | `/api/users/me/profile-picture` | Reset current user's profile picture | 204 |
| PUT | `/api/users/me/profile-picture` | Update current user's profile picture | 204 |
| POST | `/api/users/me/send-email-verification` | Send email verification | 204 |
| POST | `/api/users/me/verify-email` | Verify email | 204 |
| DELETE | `/api/users/{id}` | Delete user | 204 |
| GET | `/api/users/{id}` | Get user by ID | 200 |
| PUT | `/api/users/{id}` | Update user | 200 |
| GET | `/api/users/{id}/groups` | Get user groups | 200 |
| POST | `/api/users/{id}/one-time-access-email` | Request one-time access email (admin) | 204 |
| POST | `/api/users/{id}/one-time-access-token` | Create one-time access token for user (admin) | 201 |
| DELETE | `/api/users/{id}/profile-picture` | Reset user profile picture | 204 |
| PUT | `/api/users/{id}/profile-picture` | Update user profile picture | 204 |
| GET | `/api/users/{id}/profile-picture.png` | Get user profile picture | 200 |
| PUT | `/api/users/{id}/user-groups` | Update user groups | 200 |
| GET | `/api/users/{id}/webauthn-credentials` | List user passkeys | 200 |
| DELETE | `/api/users/{id}/webauthn-credentials/{credentialId}` | Delete user passkey | 204 |

### Version

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| GET | `/api/version/current` | Get current deployed version of Pocket ID | 200 |
| GET | `/api/version/latest` | Get latest available version of Pocket ID | 200 |

### Well Known

| Method | Path | Summary | Success |
| --- | --- | --- | --- |
| GET | `/.well-known/jwks.json` | Get JSON Web Key Set (JWKS) | 200 |
| GET | `/.well-known/oauth-authorization-server` | Get OAuth 2.0 authorization server metadata | 200 |
| GET | `/.well-known/openid-configuration` | Get OpenID Connect discovery configuration | 200 |

## Summary

113 operations across 82 paths.

## Schemas

73 definitions in the upstream document.

| Definition | Fields |
| --- | --- |
| `api.apiCimdAccessUpdateDto` | enabled, permissionIds |
| `api.apiClientAccessDto` | cimdGrantedAccess, cimdGrantedPermissionIds, client, clientAccess, clientPermissionIds, userDelegatedAccess, userDelegatedPermissionIds |
| `api.apiClientDto` | clientType, hasDarkLogo, hasLogo, id, isPublic, name |
| `api.apiClientGrantDto` | clientAccess, clientPermissionIds, userDelegatedAccess, userDelegatedPermissionIds |
| `api.apiClientGrantUpdateDto` | clientAccess, clientPermissionIds, userDelegatedAccess, userDelegatedPermissionIds |
| `api.apiCreateDto` | name, resource |
| `api.apiPermissionInputDto` | description, key, name |
| `api.apiPermissionResponseDto` | allowedForCimdClients, description, id, key, name |
| `api.apiPermissionsUpdateDto` | permissions |
| `api.apiResponseDto` | allowCimdClients, createdAt, id, name, permissions, resource |
| `api.apiUpdateDto` | name |
| `api.clientApiGrantDto` | api, cimdGrantedAccess, cimdGrantedPermissionIds, clientAccess, clientPermissionIds, userDelegatedAccess, userDelegatedPermissionIds |
| `apikey.apiKeyCreateDto` | description, expiresAt, name |
| `apikey.apiKeyDto` | createdAt, description, expirationEmailSent, expiresAt, id, lastUsedAt, name |
| `apikey.apiKeyResponseDto` | apiKey, token |
| `apperror.Code` |  |
| `devicelogin.decisionDto` | code, decision |
| `devicelogin.requestCreateDto` | expiresAt, id, interval, userCode, verificationUri, verificationUriComplete |
| `devicelogin.verificationDto` | code |
| `devicelogin.verificationInfoDto` | city, country, device, expiresAt, ipAddress, userCode |
| `dto.AccessibleOidcClientDto` | clientType, description, hasDarkLogo, hasLogo, id, lastUsedAt, launchURL, name, requiresReauthentication |
| `dto.AppConfigUpdateDto` | accentColor, allowOwnAccountEdit, allowUserSignups, appName, cimdUrlAllowlist, disableAnimations, emailApiKeyExpirationEnabled, emailLoginNotificationEnabled, emailOneTimeAccessAsAdminEnabled, emailOneTimeAccessAsUnauthenticatedEnabled, emailVerificationEnabled, emailsVerified, homePageUrl, ldapAdminGroupName, ldapAttributeGroupMember, ldapAttributeGroupName, ldapAttributeGroupUniqueIdentifier, ldapAttributeUserDisplayName, ldapAttributeUserEmail, ldapAttributeUserFirstName, ldapAttributeUserLastName, ldapAttributeUserProfilePicture, ldapAttributeUserUniqueIdentifier, ldapAttributeUserUsername, ldapBase, ldapBindDn, ldapBindPassword, ldapEnabled, ldapSkipCertVerify, ldapSoftDeleteUsers, ldapUrl, ldapUserGroupSearchFilter, ldapUserSearchFilter, requireUserEmail, sessionDuration, signupDefaultCustomClaims, signupDefaultUserGroupIDs, smtpFrom, smtpHost, smtpPassword, smtpPort, smtpSkipCertVerify, smtpTls, smtpUser, webauthnAllowSyncedPasskeys, webauthnAuthenticatorAttachment, webauthnUserVerification |
| `dto.AppConfigVariableDto` | isPublic, key, type, value |
| `dto.AuditLogDto` | actorUsername, city, country, createdAt, data, device, event, id, ipAddress, userID, username |
| `dto.AuthorizedOidcClientDto` | client, lastUsedAt, scope |
| `dto.CustomClaimCreateDto` | key, value |
| `dto.CustomClaimDto` | key, value |
| `dto.EmailVerificationDto` | token |
| `dto.ErrorDto` | code, details, error, request_id |
| `dto.OidcClientCreateDto` | accessTokenDurationMinutes, callbackURLs, credentials, darkLogoUrl, description, hasDarkLogo, hasLogo, id, isGroupRestricted, isPublic, launchURL, logoUrl, logoutCallbackURLs, name, pkceEnabled, refreshTokenDurationMinutes, requiresPushedAuthorizationRequests, requiresReauthentication, skipConsent |
| `dto.OidcClientCredentialsDto` | federatedIdentities, secrets |
| `dto.OidcClientDto` | accessTokenDurationMinutes, callbackURLs, clientType, credentials, description, hasDarkLogo, hasLogo, id, isGroupRestricted, isPublic, launchURL, logoutCallbackURLs, name, pkceEnabled, pkceSupported, refreshTokenDurationMinutes, requiresPushedAuthorizationRequests, requiresReauthentication, skipConsent |
| `dto.OidcClientFederatedIdentityDto` | audience, issuer, jwks, replayProtection, subject |
| `dto.OidcClientMetaDataDto` | clientType, description, hasDarkLogo, hasLogo, id, launchURL, name, requiresReauthentication |
| `dto.OidcClientPreviewDto` | accessToken, idToken, userInfo |
| `dto.OidcClientSecretCreateDto` | expiresAt, secret |
| `dto.OidcClientSecretCreatedDto` | createdAt, expiresAt, id, isActive, prefix, secret |
| `dto.OidcClientSecretDto` | createdAt, expiresAt, id, isActive, prefix |
| `dto.OidcClientUpdateDto` | accessTokenDurationMinutes, callbackURLs, credentials, darkLogoUrl, description, hasDarkLogo, hasLogo, isGroupRestricted, isPublic, launchURL, logoUrl, logoutCallbackURLs, name, pkceEnabled, refreshTokenDurationMinutes, requiresPushedAuthorizationRequests, requiresReauthentication, skipConsent |
| `dto.OidcClientWithAllowedGroupsDto` | accessTokenDurationMinutes, allowedUserGroups, callbackURLs, clientType, credentials, description, hasDarkLogo, hasLogo, id, isGroupRestricted, isPublic, launchURL, logoutCallbackURLs, name, pkceEnabled, pkceSupported, refreshTokenDurationMinutes, requiresPushedAuthorizationRequests, requiresReauthentication, skipConsent |
| `dto.OidcClientWithAllowedUserGroupsDto` | accessTokenDurationMinutes, allowedUserGroups, callbackURLs, clientType, credentials, description, hasDarkLogo, hasLogo, id, isGroupRestricted, isPublic, launchURL, logoutCallbackURLs, name, pkceEnabled, pkceSupported, refreshTokenDurationMinutes, requiresPushedAuthorizationRequests, requiresReauthentication, skipConsent |
| `dto.OidcUpdateAllowedUserGroupsDto` | userGroupIds |
| `dto.Pagination` | currentPage, itemsPerPage, totalItems, totalPages |
| `dto.PublicAppConfigVariableDto` | key, type, value |
| `dto.UserCreateDto` | disabled, displayName, email, emailVerified, firstName, id, isAdmin, lastName, locale, userGroupIds, username |
| `dto.UserDto` | customClaims, disabled, displayName, email, emailVerified, firstName, id, isAdmin, lastName, ldapId, locale, userGroups, username |
| `dto.UserGroupCreateDto` | friendlyName, name |
| `dto.UserGroupDto` | allowedOidcClients, createdAt, customClaims, friendlyName, id, ldapId, name, users |
| `dto.UserGroupMinimalDto` | createdAt, customClaims, friendlyName, id, ldapId, name, userCount |
| `dto.UserGroupUpdateAllowedOidcClientsDto` | oidcClientIds |
| `dto.UserGroupUpdateUsersDto` | userIds |
| `dto.UserUpdateUserGroupDto` | userGroupIds |
| `dto.WebauthnCredentialDto` | attestationType, backupEligible, backupState, createdAt, credentialID, id, name, transport |
| `github_com_pocket-id_pocket-id_backend_internal_dto.Paginated-api_apiClientAccessDto` | data, pagination |
| `github_com_pocket-id_pocket-id_backend_internal_dto.Paginated-api_apiClientDto` | data, pagination |
| `github_com_pocket-id_pocket-id_backend_internal_dto.Paginated-api_apiResponseDto` | data, pagination |
| `github_com_pocket-id_pocket-id_backend_internal_dto.Paginated-apikey_apiKeyDto` | data, pagination |
| `github_com_pocket-id_pocket-id_backend_internal_dto.Paginated-dto_AccessibleOidcClientDto` | data, pagination |
| `github_com_pocket-id_pocket-id_backend_internal_dto.Paginated-dto_AuditLogDto` | data, pagination |
| `github_com_pocket-id_pocket-id_backend_internal_dto.Paginated-dto_AuthorizedOidcClientDto` | data, pagination |
| `github_com_pocket-id_pocket-id_backend_internal_dto.Paginated-dto_OidcClientWithAllowedGroupsDto` | data, pagination |
| `github_com_pocket-id_pocket-id_backend_internal_dto.Paginated-dto_UserDto` | data, pagination |
| `github_com_pocket-id_pocket-id_backend_internal_dto.Paginated-dto_UserGroupMinimalDto` | data, pagination |
| `github_com_pocket-id_pocket-id_backend_internal_dto.Paginated-usersignup_signupTokenDto` | data, pagination |
| `onetimeaccess.emailAsAdminDto` | ttl |
| `onetimeaccess.emailAsUnauthenticatedUserDto` | email, redirectPath |
| `onetimeaccess.tokenCreateDto` |  |
| `scimsync.ScimServiceProviderCreateDTO` | endpoint, oidcClientId, token |
| `scimsync.ScimServiceProviderDTO` | createdAt, endpoint, id, lastSyncedAt, oidcClient, token |
| `usersignup.signUpDto` | email, firstName, lastName, token, username |
| `usersignup.signupTokenCreateDto` | ttl, usageLimit, userGroupIds |
| `usersignup.signupTokenDto` | createdAt, expiresAt, id, token, usageCount, usageLimit, userGroups |
| `utils.JSONDuration` |  |
