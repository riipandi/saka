# Security (OWASP)

Use the latest applicable OWASP edition. Check owasp.org if unsure, since the lists are revised periodically; the map below uses the Top 10:2021 categories as the framework (the core categories remain relevant). Add the **OWASP API Security Top 10 (2023)** for APIs, **ASVS** as the detailed checklist, and the **OWASP Top 10 for LLM Applications** if there are AI/LLM features.

## Top 10 -> what to look for

- **A01 Broken Access Control**: IDOR/BOLA (accessing others' objects by ID), authorization enforced only in the UI, privilege escalation, unguarded admin endpoints, overly permissive CORS, mass assignment, path traversal. Authorization must be checked server-side per request and per object.
- **A02 Cryptographic Failures**: sensitive data unencrypted (transit/at rest), weak algorithms (MD5/SHA1 for passwords, ECB mode), hardcoded keys, reused IVs/nonces, insecure RNG for tokens, passwords without adaptive hashing (argon2/bcrypt/scrypt).
- **A03 Injection**: SQL/NoSQL/OS command/LDAP/template/header injection, XSS (stored/reflected/DOM), unsafe deserialization, raw ORM queries built by string concatenation. Trace every input that flows into an interpreter.
- **A04 Insecure Design**: no threat model, business flows that can be abused (races on coupons/balances, enumeration, brute force), missing rate limits/quotas.
- **A05 Security Misconfiguration**: debug mode on, default credentials, missing security headers, verbose errors/stack traces, unnecessary ports/services, overly broad cloud/container permissions, XXE.
- **A06 Vulnerable and Outdated Components**: run the ecosystem's dependency vulnerability scanner, check for unmaintained dependencies, lockfiles, base image versions.
- **A07 Identification and Authentication Failures**: password policy, brute force/credential stuffing, session fixation, tokens without expiry/rotation/revocation, JWT pitfalls (alg none, weak secret, unchecked aud/iss/exp), weak password reset, MFA bypass.
- **A08 Software and Data Integrity Failures**: updates/plugins without verification, unrestricted CI/CD pipelines, deserializing untrusted data, dependencies without pinning/integrity checks.
- **A09 Logging and Monitoring Failures**: security events not logged, logs containing secrets/PII/tokens, log injection, no alerting.
- **A10 SSRF**: server fetches URLs from user input without an allowlist (including redirects, DNS rebinding, cloud metadata endpoint 169.254.169.254).

## Additional API security

BOLA/BFLA, excessive data exposure (responses with surplus fields), unrestricted resource consumption (no size/pagination/rate limits), unsafe consumption of third-party APIs, endpoint inventory (old versions/shadow APIs still live).

## Beyond the Top 10

Secrets in repo/history/CI logs (run a secret scanner), file uploads (type, size, storage location, execution), CSRF and cookie flags (HttpOnly, Secure, SameSite), open redirects, clickjacking, ReDoS, privacy/PII (retention, minimization, deletion rights), container/IaC configuration (root user, secrets in images, open ports).

## How to report

For each vulnerability: OWASP category, location, short exploitation scenario and repro steps **in a local environment only**, impact, severity (CVSS when feasible), concrete fix. Mask secret values.
