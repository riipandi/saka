# Saka Documentation

Saka is a **project boilerplate with authentication built in**: clone it,
and the hardest part of a new product — accounts, sign-in, sessions,
permissions — already works. Your product's code grows around a battle-
tested core instead of rebuilding user management from scratch.

Authentication ships in both directions:

- **Saka as the IdP** — other applications sign their users in with Saka
  accounts over OpenID Connect. Deploy Saka standalone and it is just an
  identity provider; keep building your product on it and the same
  provider serves your app's own users.
- **Saka as the SSO client** — users sign in to Saka itself with Google,
  GitHub, or any OpenID Connect provider you connect.

## Start here

- **[Product guide](product-guide.md)** — everything built in, one walk:
  every way to sign in, accounts and groups, application-facing
  surfaces, storage, notifications, audit, and the rest.
- **[Deployment](deployment.md)** — running Saka yourself.
- **[Contributing](contributing.md)** — joining the project.

## By topic

| Topic | Page | What it covers |
| --- | --- | --- |
| The whole product | [Product guide](product-guide.md) | All sign-in methods, the second factor, sessions, users, the platform surfaces |
| The built-in authentication, closely | [Authentication](auth.md) | Each method's rules, MFA and recovery codes, step-up, sessions and impersonation, lockouts and bans || Signing in with Google, GitHub, or your own provider | [OAuth SSO](oauth-sso.md) | Connecting providers, how the sign-in resolves, linking and account safety |
| Signing other apps in with Saka | [OpenID provider](oidc.md) | What connected applications can do, sign-out, getting connected |
| Which standards Saka speaks | [OIDC profiles](oidc-profiles.md) | The conformance profiles in plain language — kept, optional, dropped, and why |
| Files | [Storage](storage.md) | Buckets, resumable uploads, signed links, profile pictures |
| Outbound events | [Webhooks](webhooks.md) | Subscribing destinations, signed deliveries, retries and guards |
| In-product messages | [Notifications](notifications.md) | Audiences, read receipts, the live stream |
| Building and operating | [Debug & operator utilities](debug-utilities.md) | The debug devtools, the operator commands, the verification harnesses |
| Managing people | [Users, groups, roles](users.md) | The administrative surface: accounts, bans and locks, groups, grants, sign-up modes, blocklist |
| Machine credentials | [API keys](api-keys.md) | Creating, renewing, revoking; what a key may do and which credential fits which caller |
| Provisioning to directories | [SCIM](scim.md) | Per-client provisioning targets, when passes run, extra token claims |
| The deployment's knobs | [Configuration](configuration.md) | The config file's precedence, secrets, the settings catalog |
| The signals | [Observability](observability.md) | Health vs readiness, logs, traces and metrics, the operations console |
| The machine-facing contract | [API endpoints](api-endpoint.md), [API responses](api-response.md) | Every procedure and route, and the one contract both transports share |

## Reference

- [Changelog](changelog.md)
