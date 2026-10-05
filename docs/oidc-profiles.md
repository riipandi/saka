# Profiles: what Saka speaks, and why

OpenID Connect and OAuth come in "profiles" — standardized bundles of
features a server can promise to support. Think of them as menus: some
dishes are the main course, some are sides, and some the restaurant
deliberately took off the menu because the recipe is outdated. This page
explains, in plain terms, what each profile is for, whether Saka supports
it, and whether you should want it.

Saka plays both roles in the identity world:

- As an **identity provider** (the technical term is *OpenID Provider*,
  OP), other applications can let their users sign in **with a Saka
  account** — the way apps offer "Sign in with Google".
- As a **relying party** (RP), Saka's own sign-in page can accept
  **Google, GitHub, or any other standards-compliant provider** — "Sign in
  with Google" on Saka itself.

---

## As an identity provider

### The main course

**Basic OP** — supported, tested. This is the standard sign-in flow of
the modern web: an application sends the user to Saka, the user proves
who they are, and the application receives a one-time ticket it can trade
for proof of identity. The ticket can't be reused, and the exchange is
protected against interception. Everything else builds on this one.

**Config OP** — supported, tested. Applications can discover on their own
how to talk to Saka — where the endpoints are, which algorithms are used,
what the rules are — by reading one public document. Without it, every
application would need hand-written configuration.

### Sign-out

**RP-Initiated logout** — supported, tested. When a user clicks "Sign
out" in a connected application, their Saka session ends too. Without
it, signing out of one app leaves you silently signed in to the rest.

**Back-channel logout** — supported, tested. Saka can notify connected
applications *server-to-server* when a session ends, so apps that never
see the browser still find out. The current standards favor this method
over its older browser-based alternatives.

### Deliberately not on the menu

**Front-channel logout** and **Session management** — not supported, by
design. These are older logout mechanisms that work through hidden
frames in the browser. Modern browsers increasingly block the technique,
and the standards bodies themselves point new work toward back-channel
logout instead.

**Implicit** and **Hybrid** flows — not supported, by design. These are
carried-over methods from an earlier era that put tokens into URLs, where
they cannot be adequately protected. The latest OAuth standard (OAuth 2.1)
has retired them.

### Worth adding only on demand

**Dynamic registration** — not supported. It would let any application
register itself to accept Saka logins automatically, without an
administrator's approval. That suits vast open ecosystems; for Saka's
model, where a known set of applications is registered by an
administrator, the manual step is a feature, not a gap. (For internal AI
tooling, see the MCP section below.)

**Form Post responses** — not supported. An alternative technical way for
an application to receive its ticket, needed mainly by some older
applications. There is no current demand.

---

## As a relying party

**Sign in with Google, GitHub, or your own provider** — supported. The
sign-in page can hand the user off to an external provider and verify
what comes back, with the same anti-theft protections Saka requires of
others (single-use tickets, interception protection, verified identity
statements). Adding a new provider is a matter of pointing Saka at its
discovery document.

Not needed, and why: profiles covering registration automation, alternate
ticket delivery, and cross-service sign-out have no user-facing role when
the provider list is small and deliberately chosen.

---

## For internal AI tooling (MCP)

AI agents and assistants connect to tools through an open protocol
called MCP. An MCP server that needs protection is, in OAuth terms, just
another application that accepts Saka tokens — so the profiles above are
exactly what it needs, and they are already in place:

- The sign-in flow an MCP client walks is the same tested flow as Basic
  OP — including the interception protection MCP clients refuse to work
  without.
- Each MCP tool should receive tokens that only work for *that* tool —
  a token meant for one tool must be worthless at the next. The
  infrastructure to issue such tokens exists in Saka's engine; turning
  it on is planned work when the first MCP servers land.
- Applications registering themselves automatically stays off the table
  here too: the MCP standard itself prefers pre-registered credentials.

---

## Where the numbers live

The full checklist — every profile, its support status, its test results
against the OpenID Foundation's official conformance suite, and the
recommendation tags (keep / optional / drop) — is maintained in the
repository's development notes. For this page, the short version:

| | Supported | Tested against the official suite | Recommendation |
| --- | --- | --- | --- |
| Basic OP (standard sign-in) | yes | yes, passing | keep |
| Config OP (self-discovery) | yes | yes, passing | keep |
| RP-Initiated logout | yes | yes, passing | keep |
| Back-channel logout | yes | yes, passing | keep |
| Dynamic registration | no | — | only on demand |
| Form Post responses | no | — | only on demand |
| Implicit / Hybrid flows | no | — | dropped by the standard |
| Front-channel logout / Session management | no | — | dropped by design |
| Sign in with Google / GitHub / custom | yes | not yet | keep |
