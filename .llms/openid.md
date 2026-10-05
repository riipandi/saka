# OpenID Connect

## Conformance Profiles

### OpenID Provider Servers

Status after the certification-readiness phases (`.llms/plans/plan-20261005_1845.md`);
a tick means implemented and locally rehearsal-green against the OIDF suite —
the hosted submission run is phase 6's runbook. The OP side is the inbound
surface (`modules/federation/oidc`); the RP side is the outbound one
(`modules/identity/oauthsso`), listed under Relying Party below.

- [x] Basic OP — implemented; local rehearsal 35/35
- [x] Config OP — implemented; local rehearsal 1/1 (WARNING = pass)
- [ ] Dynamic OP — not implemented (no dynamic client registration surface)
- [ ] Form Post OP — not implemented (`response_mode=form_post` absent)
- [ ] Hybrid OP — not implemented (no `code id_token` hybrid flows)
- [ ] Implicit OP — not implemented (OAuth 2.1 dropped the implicit grant)

### OpenID Providers for Logout Profiles

- [x] RP-Initiated OP — implemented; local rehearsal 11/11
- [x] Back-Channel OP — implemented; local rehearsal 2/2
- [ ] Front-Channel OP — rejected: iframe delivery contradicts the no-embedded-frame design (plan D1)
- [ ] Session OP — rejected: session-management iframes contradict the no-browser-session design (plan D1)

### Relying Party

Saka is also a relying party: sign-in with Google, GitHub, or a custom
OIDC provider rides the outbound surface (`modules/identity/oauthsso`) —
the authorization code flow with PKCE S256, state, an OIDC nonce the
`id_token` must answer, discovery-based connections, a verified
`id_token` (userinfo overrides on OAuth2-only providers), and refresh
rotation. None of the RP profiles has been rehearsed against the suite —
the suite runs them as the `oidcc-client-*` plans, a separate harness
this plan did not build.

- [x] Basic RP — implemented (code flow + PKCE S256 sign-in); not rehearsed
- [x] Config RP — implemented (discovery-based connections); not rehearsed
- [ ] Dynamic RP — not implemented (a sign-in client never registers itself)
- [ ] Form Post RP — not implemented (`response_mode=form_post` unused)
- [ ] RP-Initiated RP — not implemented (sign-in never ends the upstream session)
- [ ] Back-Channel RP — not implemented (no logout-token receiver)

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
