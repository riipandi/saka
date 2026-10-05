# API keys

API keys are machine credentials: a background service, a script, or an
integration that calls Saka's API with no human and no browser present.

## The shape of a key

A key is one string in two parts — a public **prefix** and a **secret** —
created by a signed-in user and bound to that user's identity:

- The raw key is shown **exactly once**, at creation; the database stores
  only its hash, so a leaked database cannot replay a key.
- The name is unique per owner.
- The key carries an expiry window in the future; renewal is how a key
  lives past its expiry — an expired key earns a new secret and a new
  window, a live one refuses renewal.
- Revocation is soft and idempotent: the key stays listed with its
  revocation stamp, a second revocation changes nothing, and a key
  belonging to another account is simply not found.

## What a key may do

A key acts **as its owner**, with the owner's permissions — but inside a
narrower lane: a key cannot manage keys, cannot enroll passkeys (a
machine credential has no browser to enroll one on), and is refused on
the surfaces that need a human's proof. A signed-in user sees their own
keys, ordered and stamped with last use; an administrator sees every key
the deployment holds and who owns each.

## When to reach for which credential

| Caller | Credential | Page |
| --- | --- | --- |
| A user's browser or app | The session token pair | [Authentication](auth.md) |
| A service acting for a user | An OAuth client with the user's consent | [OpenID provider](oidc.md) |
| A machine with its own identity | Client-credentials tokens | [OpenID provider](oidc.md) |
| A script or integration on behalf of a user | An API key | this page |
| A one-off guest | A one-time access code | [Authentication](auth.md) |

The route table: [API endpoints](api-endpoint.md#api-key).
