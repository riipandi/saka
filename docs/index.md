# Saka Documentation

Welcome. Saka is a self-hosted identity and application platform: it
keeps your users' accounts safe, signs them in — by itself, or through
the providers they already trust — and gives developers the machinery
around those accounts: files, notifications, audit trails, and an API.

## Start here

- **[Product guide](product-guide.md)** — the whole product in one walk:
  every way to sign in, accounts and groups, letting other applications
  in, storage, notifications, audit, and the rest.
- **[Deployment](deployment.md)** — running Saka yourself.
- **[Contributing](contributing.md)** — joining the project.

## By topic

| Topic | Page | What it covers |
| --- | --- | --- |
| Signing in with Google, GitHub, or your own provider | [OAuth SSO](oauth-sso/index.md) | Connecting providers, how the sign-in resolves, linking and account safety |
| Signing other apps in with Saka | [OpenID Provider](oidc-provider/index.md) | What connected applications can do, sign-out, getting connected |
| Which standards Saka speaks | [Profiles](oidc-provider/profiles.md) | The conformance profiles, in plain language — what's kept, what's dropped, why |
| The machine-facing contract | [API endpoints](api-endpoint.md), [API responses](api-response.md) | Every procedure and route, and the one contract both transports share |

## Reference

- [Changelog](changelog.md)
