# Saka Product Guide

Saka is a self-hosted identity and application platform: it keeps your
users' accounts safe, signs them in (by itself, or through the providers
they already trust), and gives developers the machinery around those
accounts — files, notifications, audit trails, and an API — behind one
roof.

This guide walks through what the product does. Protocol details live in
the [API endpoints](api-endpoint.md) reference; the identity-provider
surface has its own pages under [oidc-provider](oidc-provider/index.md)
and [oauth-sso](oauth-sso/index.md).

---

## Signing in

Saka offers several ways in, and each account can carry several at once —
so losing one never locks the person out.

- **Password** — the classic: an email address or username plus a
  password. Passwords can be required to meet length and character-class
  rules.
- **Passkeys** — the modern replacement: the account unlocks with the
  device's own biometrics or security key. A user can add a passkey,
  sign in with one, and even *remove their password entirely* once a
  passkey or linked provider exists — the system refuses to leave an
  account with no way back in.
- **One-time email codes** — sign in with just an email address and a
  short code. Every email-based flow (verification, sign-in, password
  reset) delivers a **single-use code, never a link** — links don't leak
  through preview bots and referrer headers; codes can't be replayed.
- **External providers** — "Continue with Google/GitHub" or any
  OpenID Connect provider. See [OAuth SSO](oauth-sso/index.md).
- **One-time access links for guests** — grant a person access without
  an account for a bounded time (a contractor, a demo): a scoped,
  expiring credential instead of a shared login.

**The second factor.** Accounts can require a TOTP authenticator app
(the rotating six-digit kind), with printable recovery codes as the way
back in. When an account has multi-factor on, *every* sign-in path —
password, one-time code, external provider — pauses at the same
second-factor step. Sensitive actions can demand a fresh proof (step-up)
even for an already signed-in session.

**Device sign-in.** A device with no keyboard of its own (a TV, a kiosk)
displays a short code; the user enters it on their normal browser and
approves — the device signs in without ever seeing the password.

**Sessions.** Users can see every device signed in as them, revoke one,
sign out everything else, or sign out everywhere. "Remember me" picks a
longer session window. Administrators can open a support session on a
user's behalf (impersonation), which is its own visible, revocable
session.

**Recovery.** Forgot-password flows send single-use codes. Administrators
can reset a user's password directly, which signs that user out
everywhere and leaves an audit record.

---

## Who's who: accounts, groups, roles

- **Users** hold a verified email, an optional username, names, a photo,
  and per-user settings such as whether they may delete their own
  account.
- **Groups** collect users (an "editors" group, say). Groups can be
  granted access to things as a whole.
- **Roles and permissions** are the access system's vocabulary: named
  capabilities (like "create notifications") are granted to roles or
  directly to a user, and everything the API can do checks this catalog
  before it answers.
- **Blocklist** can refuse sign-ups (and optionally sign-ins) for
  particular emails or domains — disposable-address control for open
  sign-up deployments.

---

## Letting other applications in

- **OpenID provider** — other applications sign their users in with
  Saka accounts: the standard code flow with modern protections, user
  consent, refresh tokens, machine-to-machine credentials, and a device
  flow for TVs and CLIs. See [oidc-provider](oidc-provider/index.md).
- **Connected clients** — administrators manage the registered
  applications (their callbacks and secrets), users manage their own
  consents, and extra token claims can be shaped per application.
- **SCIM** — an organization's user directory can synchronize accounts
  and group memberships into Saka automatically.
- **API keys** — machine callers authenticate with keys an administrator
  issues (one visible at creation, then never again), each carrying its
  own expiry and access.
- **Webhooks** — when something happens in Saka, other systems can hear
  about it: signed event deliveries to registered URLs.

---

## The rest of the platform

- **Storage** — file buckets with uploads that survive bad networks
  (resumable uploads), signed download links that expire, and per-bucket
  access rules.
- **Notifications** — in-product announcements and targeted notices,
  delivered to everyone, to a group, or to one user, with read receipts.
- **Audit log** — every security-relevant act (a sign-in, a password
  change, a role grant, a revoked session) is recorded with who did it,
  what, and when — searchable by administrators.
- **Settings** — the deployment's switches (sign-up open or invite-only,
  verification rules, session windows, retention) live in one
  administrator-facing place, with safe defaults.

---

## Where to go next

- [OAuth SSO](oauth-sso/index.md) — connecting external providers for
  sign-in
- [OpenID Provider](oidc-provider/index.md) — how other apps sign users
  in with Saka, and [Profiles](oidc-provider/profiles.md) — which
  conformance profiles Saka supports and why
- [API endpoints](api-endpoint.md) and [API responses](api-response.md) —
  the machine-facing contract
- [Deployment](deployment.md) — running Saka yourself
- [Contributing](contributing.md) — joining the project
