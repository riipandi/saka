# OpenID Provider

Saka's auth core is a full OpenID provider. Out of the box, other
applications — your own product's siblings, a customer's integration, a
CLI tool — can sign their users in **with Saka accounts**: the standard,
standards-tested protocol (OIDC Core, OAuth 2.0/2.1) that any
standards-compliant application already speaks.

And because it is a boilerplate, the provider is not a separate product
you bolt on — it is the same accounts, the same sessions, and the same
consent pages your own app already uses. Deploy Saka standalone and it
is just an identity provider; build on it and the same provider serves
your product.

New here? Start with [Profiles](oidc-profiles.md) — what Saka supports,
what it deliberately doesn't, and why, in plain terms.

## What an application can do

**Sign users in with authorization codes.** An application redirects the
user to Saka, the user signs in (or is already signed in), and the
application receives a one-time code it exchanges for tokens. Code
interception is mitigated with PKCE (the S256 form only), and every code
works exactly once — a replayed code revokes the grant it belongs to.

**Verify who the user is.** The ID token names the user, the issuer, and
when the authentication happened. The `auth_time` claim tells the
application how fresh the sign-in was, and a `max_age` request forces a
fresh one when the old authentication is too stale.

**Ask for the user's consent once.** The first sign-in shows a consent
page; after that, the application's access is remembered and later sign-ins
are silent. Users can withdraw that consent, and the next sign-in asks
again.

**Read the user's profile.** The userinfo endpoint answers with the claims
the user's scopes allow — name, email, and the standard profile fields.

**Stay signed in on the user's behalf.** Refresh tokens let an application
renew access without the user's presence. A refresh token that is used
twice — the second, replayed use — kills the family it belongs to.

**Act as a machine.** Non-human clients (a CLI, a background service)
authenticate with their own credentials and receive tokens directly, with
no user in the loop.

**Sign in on a second device.** The device flow shows a code on the device
asking to sign in; the user confirms on a device that has a browser.

**Revoke access.** A client can revoke its own tokens; administrators can
inspect and revoke clients' access from the management API.

## Signing users out

An application can send the user's browser to the end-session endpoint. If
the application registers a post-logout destination, the user lands there
afterwards. For applications that sign users out server-to-server, Saka
delivers a signed logout token to each registered callback so the other
side can end its own session — this is off by default and turned on per
deployment.

## Connecting an application

An administrator registers the application first: its identifier, its
callback URLs, and the credentials it authenticates with. Registration is a
management-API operation; the endpoints live under `/oidc/`, and the
machine-readable metadata at `/.well-known/openid-configuration` describes
everything a client library needs — endpoints, algorithms, scopes, and
claims. Most platforms' standard OIDC libraries work with no custom code.

## Extra claims for a client

Beyond the standard profile claims, an administrator can attach **custom
claims** to a user or a group — a value of the deployment's choosing
(an employee ID, a department, a tier) that the client's tokens and
userinfo answers carry. Claim keys are suggested from a catalog the
deployment knows, listed per subject, and replaced or removed as rows —
and a preview procedure shows exactly which claims a given user would
receive before anything is committed.

## Conformance

Saka is rehearsed against the OpenID Foundation's conformance suite, run
locally over TLS: the Basic OP, Config OP, RP-Initiated OP, and
Back-Channel OP profiles all pass. The hosted certification run is the
project owner's; its runbook lives with the development notes.

---

Back to [Documentation Index](./index.md)
