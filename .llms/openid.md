# OpenID Connect

## Conformance Profiles

### OpenID Provider Servers

Status after the certification-readiness phases (`.llms/plans/plan-20261005_1845.md`);
a tick means implemented and locally rehearsal-green against the OIDF suite —
the hosted submission run is phase 6's runbook.

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

Saka is a provider, not a relying party — the RP profiles are out of scope.

- [ ] Back-Channel RP
- [ ] Basic RP
- [ ] Config RP
- [ ] Dynamic RP
- [ ] Form Post RP
- [ ] RP-Initiated RP

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
