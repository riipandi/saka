# Managing users, groups, and roles

The administrator's day-to-day: creating accounts, deciding who holds
which capabilities, and gathering people into groups. Everything here is
the administrative API — the same one the web console uses.

## Accounts

An administrator creates, updates, and deletes accounts; creation can
join the new account to groups in the same breath. A deleted account
cannot be the signed-in one, and removal keeps a soft-deleted copy of
the record.

| Act | What happens beside it |
| --- | --- |
| **Ban** | A recorded restriction ends the account's live sessions immediately |
| **Unban** | Lifts it |
| **Unlock** | Clears a lockout and the failed-attempt streak |
| **Password reset** | Triggers the account's reset flow; signs them out everywhere |
| **Impersonation** | Opens a delegated support session — see [Authentication](auth.md#sessions) |

The account holder's own moves — profile updates, password add/remove,
email change, self-delete — obey the same rules an end user sees
([Authentication](auth.md)).

## Groups

A group gathers users and is addressed as a whole — by notifications,
by an application's access restriction, by provisioning. A group carries
no permission of its own.

- Membership updates are **replaces**: the request names the entire
  member set, so there is no drift between "what I meant" and "what
  stayed".
- The administrator lists, creates, updates, and deletes groups.

## Roles and permissions

Capabilities are named ("create notifications", say) and granted to
**roles** or **directly to a user**. The grant decides; everything the
API can do checks the catalog before it answers — so an integration or
an API key inherits exactly what its owner's grants say, no more.

The permission catalog is code-defined (a capability ships with the
feature that owns it); what an administrator shapes is who holds which
grant.

## Who may sign up

Three shapes, chosen by configuration:

| Mode | Behavior |
| --- | --- |
| **Open** | Anyone may create an account — the allowlist and blocklist decide which addresses may |
| **Invite (signup tokens)** | An administrator mints single-use signup tokens; the raw token is shown once and the sign-up page asks for it |
| **Closed** | Accounts are created by an administrator only |

The mode and its lists are settings — see
[Configuration](configuration.md#configuration-vs-settings---two-different-things)
for the two-layer story.

## Blocklist

For open sign-up deployments: entries are exact email addresses or exact
domains (no wildcards; a subdomain is a different domain), an entry
carries over to subaddressed variants, and the refusal reads as the
generic "sign-up is not available" — the blocklist never becomes an
account enumerator. An optional switch extends the lists to sign-ins.
The route table: [API endpoints](api-endpoint.md#blocklist-saka-only).

---

Back to [Documentation Index](./index.md)
