# OpenID Connect

## Conformance Profiles

### OpenID Provider Servers

Status after the certification-readiness phases (`.llms/plans/plan-20261005_1845.md`);
a tick means implemented and locally rehearsal-green against the OIDF suite —
the hosted submission run is phase 6's runbook. The OP side is the inbound
surface (`modules/federation/oidc`); the RP side is the outbound one
(`modules/identity/oauthsso`), listed under Relying Party below.

The recommendation tags follow current OAuth 2.1 and OpenID guidance:
**keep** = the certification's core, hold onto it; **optional** = pick up
only when a real client needs it; **drop** = do not pursue — the standard
is retiring it.

- [x] Basic OP — implemented; local rehearsal 35/35 — **keep** (the core
  the rest builds on: OAuth 2.1's code flow with mandatory PKCE S256)
- [x] Config OP — implemented; local rehearsal 1/1 (WARNING = pass) —
  **keep** (discovery is how clients connect today; near-free beside Basic)
- [ ] Dynamic OP — not implemented (no dynamic client registration surface) —
  **optional** (only for an open ecosystem where clients self-register;
  admin registration serves saka's model — and even MCP, the spec's own
  priority order prefers pre-registration, see below)
- [ ] Form Post OP — not implemented (`response_mode=form_post` absent) —
  **optional** (some legacy SPAs and bridge products ask for it; nothing in
  the current client set does)
- [ ] Hybrid OP — not implemented (no `code id_token` hybrid flows) —
  **drop** (OAuth 2.1 retires the hybrid response types)
- [ ] Implicit OP — not implemented (OAuth 2.1 dropped the implicit grant) —
  **drop** (deprecated by OAuth 2.1 and the OAuth 2.0 Security BCP; tokens
  in the URL fragment cannot be kept confidential)

### OpenID Providers for Logout Profiles

- [x] RP-Initiated OP — implemented; local rehearsal 11/11 — **keep**
  (the baseline logout: an RP ends the session it started; browser-driven,
  no iframes)
- [x] Back-Channel OP — implemented; local rehearsal 2/2 — **keep**
  (the logout notification the current guidance favors: server-to-server,
  no browser or iframe involved)
- [ ] Front-Channel OP — rejected: iframe delivery contradicts the
  no-embedded-frame design (plan D1) — **drop** (browsers' third-party-
  context blocking breaks iframes; the ecosystem's guidance prefers
  back-channel)
- [ ] Session OP — rejected: session-management iframes contradict the
  no-browser-session design (plan D1) — **drop** (deprecated in practice;
  the OIDF's own guidance points new work at back-channel)

### Internal MCP servers (planned integration)

The MCP authorization spec (2025-11-25) makes an MCP server an OAuth 2.1
resource server and the MCP client an OAuth 2.1 client — the authorization
server (saka) needs nothing outside the profiles above:

- **Basic OP + Config OP are sufficient as-is.** MCP's flow is exactly the
  code flow with PKCE S256 the two profiles certify; MCP clients refuse to
  proceed unless the discovery document names
  `code_challenge_methods_supported` — saka's does (`["S256"]`).
- **Dynamic OP stays optional even here.** The current spec demotes DCR to
  a MAY for backwards compatibility: MCP clients prefer pre-registered
  credentials (saka's model), then Client ID Metadata Documents (a
  draft-ietf mechanism for strangers), then DCR as the fallback. Enable
  DCR only if the MCP fleet grows past admin-managed registration — and
  then behind an initial access token.
- **RFC 8707 resource indicators are the piece to enable.** MCP clients
  MUST send `resource` and MCP servers MUST validate the token's audience
  against their own canonical URI — without it, one MCP server accepts
  tokens minted for another. The engine supports it out of the box
  (`provider.WithResourceIndicators`, `WithResourceIndicatorsRequired` in
  `pkg/provider/option.go:1588`); not yet enabled on saka's provider.
- **Protected Resource Metadata (RFC 9728) lives on the MCP servers**, not
  on saka: each MCP server advertises its authorization server (saka) and
  required scopes via the `WWW-Authenticate` challenge or a well-known
  URI. Not an OP-side profile.
- **Logout profiles stay irrelevant** — MCP sessions live in the AI host
  and end with token expiry or revocation; there is no browser session to
  sign out. The refresh-token rotation saka already performs covers the
  spec's public-client requirement.

### Relying Party

Saka is also a relying party: sign-in with Google, GitHub, or a custom
OIDC provider rides the outbound surface (`modules/identity/oauthsso`) —
the authorization code flow with PKCE S256, state, an OIDC nonce the
`id_token` must answer, discovery-based connections, a verified
`id_token` (userinfo overrides on OAuth2-only providers), and refresh
rotation. None of the RP profiles has been rehearsed against the suite —
the suite runs them as the `oidcc-client-*` plans, a separate harness
this plan did not build.

- [x] Basic RP — implemented (code flow + PKCE S256 sign-in); not rehearsed —
  **keep** (the sign-in-with-Google/GitHub/custom surface saka already ships)
- [x] Config RP — implemented (discovery-based connections); not rehearsed —
  **keep** (a new provider costs a discovery URL, not a config table)
- [ ] Dynamic RP — not implemented (a sign-in client never registers itself) —
  **drop** (meaningless for a fixed set of pre-chosen providers)
- [ ] Form Post RP — not implemented (`response_mode=form_post` unused) —
  **optional** (only if a future provider rejects the query response mode)
- [ ] RP-Initiated RP — not implemented (sign-in never ends the upstream session) —
  **optional** (worth it only if saka's sign-out should also sign the user
  out of Google et al. — usually not what users expect)
- [ ] Back-Channel RP — not implemented (no logout-token receiver) —
  **optional** (pick up beside an RP-initiated receiver if a provider
  signals its logouts server-to-server)

## References

### Fundamentals and How It Works

- [What Is OpenID and OpenID Connect?](https://openid.net/developers/discover-openid-and-openid-connect/)
- [How OpenID Connect Works](https://openid.net/developers/how-connect-works/)
- [AB/Connect Working Group](https://openid.net/wg/connect/)
- [OAuth 2.1 Draft (IETF)](https://datatracker.ietf.org/doc/draft-ietf-oauth-v2-1/)
- [OAuth 2.0 (RFC 6749)](https://datatracker.ietf.org/doc/html/rfc6749)

### Certification and Testing

- [Certification Program Overview](https://openid.net/certification/)
- [OP Test Guide](https://openid.net/certification/connect_op_testing/)
- [OP Logout Test Guide](https://openid.net/certification/connect_op_logout_testing/)
- [RP Test Guide](https://openid.net/certification/connect_rp_testing/)
- [Profile Definition Document](https://openid.net/wordpress-content/uploads/2018/06/OpenID-Connect-Conformance-Profiles.pdf)
- [Testing Tool (Conformance Suite)](https://www.certification.openid.net)
- [FAPI OP Test Guide](https://openid.net/certification/certification-fapi_op_testing/)
- [All OpenID Certified Implementations](https://openid.net/certification/all-certified-implementations/)

### Fees, Policies, and Contact

- [Certification Policy for Open-Source Projects](https://openid.net/certification/open-source-project-certification-policy/)
- [Certification Fee Structure](https://openid.net/certification/fees/)
- [List of Certified Implementations](https://openid.net/certification/certified-openid-connect-implementations/)
- [Certification Team Contact](mailto:certification@oidf.org)
