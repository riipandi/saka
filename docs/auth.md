# Authentication in depth

Every account in Saka can carry several credentials at once, and every
sign-in path passes the same gates: the second factor when enrolled, the
same session rules, the same audit trail. This page walks each method and
the machinery around them.

## The sign-in methods

| Method | What the user does | The rules it obeys |
| --- | --- | --- |
| **Password** | Types an email or username and a password | Length and character-class rules; "remember me" picks the long session window |
| **Passkey** | Unlocks with the device's fingerprint, face, or security key | The credential is a WebAuthn ceremony — the server never sees a secret, only a proof it can verify |
| **One-time email code** | Types an email address, receives a short code | The code is single-use, stored only as a hash, and dies in minutes (six characters for short windows, twelve for longer ones) |
| **External provider** | Clicks "Continue with Google/GitHub/custom" | See [OAuth SSO](oauth-sso.md) |
| **Device pairing** | A device shows a code (`XXXX-XXXX`); another browser approves it | One decision per request; the code and the pairing secret live only as hashes |

Two flows pause mid-way and ask for more:

- **Unverified email.** A provider that returns an unverified address, or
  a flow that needs one, asks the user to prove the address with an
  emailed code. Three wrong codes end the flow.
- **Missing names.** If the account requires first and last names and
  nothing supplied them, the flow asks — it never guesses.

## The second factor (MFA)

An account can enroll a TOTP authenticator (the rotating six-digit apps).
From then on, **every** sign-in method pauses at the same second-factor
step: password, email code, external provider, all of them. The first
sign-in after enrollment is no exception.

| Secret | Shown | Stored |
| --- | --- | --- |
| TOTP secret (the QR setup) | once, at enrollment start | encrypted |
| Recovery codes | once, at enrollment confirm | hashed |
| Replacement recovery codes | once, at regeneration | hashed |

A pending second-factor step lives five minutes and forgives three wrong
codes. Recovery codes work both as the second factor and — one at a
time — as a standalone proof when the authenticator is gone. Turning MFA
off, or deleting an authenticator, requires a fresh second-factor proof;
an administrator can disable a user's MFA without one (support cases),
and that is audited.

## Step-up proof

Some actions are too sensitive for an old session — removing a
credential, unlinking a provider, changing what protects the account.
These ask for a **fresh proof of the current person**: the password, a
passkey tap, or an emailed code. The proof mints a single-use token that
lives thirty minutes; once presented, the sensitive action proceeds.

## Sessions

| Question | Answer |
| --- | --- |
| How long? | A short window by default; "remember me" extends it |
| Refresh? | The token pair refreshes on demand — and the refresh token itself rotates on every use |
| Can a user see their sessions? | Yes — every device signed in as them |
| Sign out? | One session, the other sessions, or everything at once |

**Impersonation** is a special session an administrator opens on a
user's account — the support tool. Its shape:

- It opens a **new** session on the target account; the delegation is
  recorded on the session and in the audit log, and the token names the
  acting administrator.
- An administrator cannot impersonate another administrator, themselves,
  or a nonexistent account.
- Ending it returns the administrator to their own session.
- While impersonating, the delegate is refused on **self-service
  security procedures** — they cannot change the target's credentials or
  consent to anything irreversible on their behalf.
- The user's own audit log shows the impersonation like any other event.

## Recovery and lockouts

- **Forgot password** — a reset request emails a 256-bit token (stored
  only as a hash). Asking for an unknown address answers exactly the
  same as a known one: the response cannot be used to learn who has an
  account.
- **Administrator reset** — an administrator can trigger the same reset
  for one account, which signs that account out everywhere.
- **Lockout** — repeated failed sign-ins lock the account; an
  administrator lifts the lock and clears the failed-attempt streak.
- **Ban** — a ban is a recorded restriction that ends the account's live
  sessions immediately; lifting it is equally explicit.
- **Self-delete** — a user can delete their own account when the
  deployment allows it (a per-account override exists); the account is
  removed and its record is kept as a soft-deleted copy.

## One-time access for guests

For a person who needs in once without an account: an administrator
mints a code (or an emailed one-time link-less code), the guest exchanges
it for a session, and it cannot be used again. The email path is a
separate switch, off by default — and its response to an unknown address
is identical to a known one, so it cannot enumerate accounts either.

---

Where next: [OAuth SSO](oauth-sso.md) adds the external-provider methods;
the machine-facing surface for everything above is
[API endpoints](api-endpoint.md).

---

Back to [Documentation Index](./index.md)
