# Managing users, groups, and roles

The administrator's day-to-day: creating accounts, deciding who holds
which capabilities, and gathering people into groups. The web console —
the **Administration** section in the sidebar — puts all of it behind a
sign-in that carries the administrator role; a visitor without it is
simply not shown the section, and the screens themselves stay out of
reach. Everything here is also the administrative API — the same one the
console uses.

## Accounts

The **Users** screen lists every account — searchable, sortable, paged —
and opens each one into its own page: the profile, the ban, the
credentials, the delegated support session, and the account's own audit
trail, one card per concern.

- **Create** adds an account with its name, address, and credential; the
  membership of groups is set from the group's side, so an account can be
  created before any group is chosen.
- **Ban** demands its reason (and may carry an expiry) and ends the
  account's live sessions immediately; **Unban** lifts it; **Unlock**
  clears a lockout and the failed-attempt streak.
- **Password reset** triggers the account's reset flow — the four states
  it can answer (no such account, no password credential, a resend
  cooldown, mail unavailable) are the screen's answers too.
- **Credentials** renames or removes a passkey and turns multi-factor
  off, each act audited.
- **Impersonation** opens a delegated support session — the screen asks
  for the reason the audit record keeps, and the orange banner that
  appears names the borrowed account until you stop. See
  [Authentication](auth.md#sessions).
- **Delete** refuses when the account is the one you are signed in as,
  and otherwise archives the row and ends its sessions.

## Groups

The **Groups** screen manages the groups: create, rename, delete, and —
per group — the **Members** dialog, where the member set is saved as a
whole (an account you uncheck leaves the group when you save, an account
you check joins), and the list of **OIDC clients** the group's members
may use.

A group gathers users and is addressed as a whole — by notifications,
by an application's access restriction, by provisioning. A group carries
no permission of its own.

## Roles and permissions

The **Roles** screen manages the named capability sets: create a role
(its slug never changes afterward), rename it, grant it permissions from
a checkbox tree grouped by the catalog's own resources, or delete it. A
role the application depends on — the built-in **Administrator** role —
opens read-only: its permission set is the installation's, not yours to
reshape.

One rule the screens keep reminding you of: **grant changes move at the
account's next sign-in or token renewal, not in place** — an account
carrying a grant you just changed keeps the old shape until its next
token. Editing your *own* grants from these screens is allowed (the
server refuses nothing), so the screen says the same thing about your
own next token.

Direct grants — a permission handed to one account outside any role —
live on the account's page beside its roles, same tree, same rule.

## Who may sign up

Three shapes, chosen by configuration:

| Mode | Behavior |
| --- | --- |
| **Open** | Anyone may create an account — the allowlist and blocklist decide which addresses may |
| **Invite (signup tokens)** | An administrator mints single-use signup tokens; the raw token is shown once and the sign-up page asks for it |
| **Closed** | Accounts are created by an administrator only |

The **Signup tokens** screen mints the invitations: a lifetime (an hour
to thirty days), an optional usage budget (unset means the single
invitation), and the groups every account created under the token joins.
The raw token appears exactly once at creation; after that the screen
shows only the usage counter and lets you revoke an unspent one. The
public sign-up page takes its shape from the deployment's answers — in
invite mode it asks for the token (an invitation link carries it), the
username field appears only when the deployment enables the username
identity, and an account created under an armed verification gate lands
on the code-entry screen instead of the sign-in door.

The mode and its lists are settings — see
[Configuration](configuration.md#configuration-vs-settings---two-different-things)
for the two-layer story.

## Blocklist

For open sign-up deployments: the **Blocklist** screen lists the
refused identifiers and adds or removes entries — an exact email address
or an exact domain (`@example.com`, no wildcards; a subdomain is a
different domain), an entry carries over to subaddressed variants, and
the refusal reads as the generic "sign-up is not available" — the
blocklist never becomes an account enumerator. An optional switch
extends the lists to sign-ins.
The route table: [API endpoints](api-endpoint.md#blocklist-saka-only).

## The audit trail

The **Audit** screen is the read side of the record: every
security-relevant act across the deployment, newest first, one row per
act with the outcome (success or failed) marked. The filters above the
list offer the events the record actually holds and the accounts that
actually have activity, plus a search over names and addresses. A
delegated act — something done while impersonating — names the
administrator behind it on the row.

---

Back to [Documentation Index](./index.md)
