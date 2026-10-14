---
status: open
captured: 2026-10-14T09:49:00+07:00
updated: 2026-10-14T10:25:00+07:00
summary: "The reconciliation of the fifteen `status: partial` archived handovers (2026-09-22 → 2026-09-30) plus plan-20261012_0155's `partially-done` phase: nearly every claim landed since. The one open residue (`key:rotate`) is plan-20261014_0955's work; the two parked decisions moved to the roadmap 2026-10-14 (RI-7, RI-8); one item resolved on re-verification."
count: 2
---

# Residues from the archived `partial` handovers (Sep 22 – Sep 30)

The fifteen archived handovers carrying `status: partial` (2026-09-22 →
2026-09-30) and plan-20261012_0155's `partially-done` phase 5 were
reconciled against the tree on 2026-10-14. What landed since — the health
checks, the mailer and its durable-queue email pass, the per-package
metrics, notification, webhooks, impersonation, password recovery, the
session-rotation reuse guard, the delete-hash guards, the api-key renew
guard, the storage hash-reuse guard, the algorithm-pinned verifier
(`pkg/jwtutils/access.go`), the lifecycle shutdowns, the rate-limit mount
(test-pinned), the GeoIP resolution, the consent screen — is recorded
here only as settled. These are the residues.

- status: open
  area: cli / appkey
  summary: `key:rotate` prints `not yet implemented` and exits
    (`cmd/appkey.go:177`) — the stale-encryption-key rotation the mailer
    handover promised never landed; `key:generate` beside it works.
  trigger: The first deployment that must rotate an encryption key
    (compromised key material, or a cipher migration).

- status: resolved (2026-10-14, on re-verification)
  area: backend/config + queue
  summary: The `queue.release_after` invariant is enforced at the right
    seam — `framework/queue/client.go:131-139` `Register` panics when a
    queue's `Timeout >= ReleaseAfter`, with the comment naming why the
    comparison belongs there ("Both values are known here and nowhere
    else"): `config.Validate` cannot see task timeouts, which live in the
    job registrations. My earlier reconciliation read only
    `internal/config/validate.go:267` and missed the client's guard.
  resolution: No work owed — the composition root refuses to boot a
    wiring that pairs a long task with a short release window; the
    default (one hour above the 30-minute chunk-upload timeout) keeps the
    shipped shape safe.
  trigger: None — enforced.

## Reconciled and settled (no work owed)

- handover-20260922_1941 (health), 20260923_0320 (mailer), 20260924_0100
  (serve lifecycle), 20260925_1455 (instrumentation), 20260926_2052
  (aspirational modules), 20260927_0320/1645 (impersonation, parity,
  CORS), 20260927_1759 (notification), 20260928_0236 (the audit's five
  identity cuts and three side findings), 20260929_1700/1925 (the
  federation audit and plan-20260929_1722's remediation), 20260930_1355
  (webhooks), and plan-20261012_0155 phase 5 (the consent screen the
  owner is building now; issue-20261012_0445 no longer live) — all
  verified landed in the 2026-10-14 tree.
- Housekeeping note, not an item: the handover-20260929_1925 "Yaak
  mirror" sync is now guarded by the pre-commit Yaak check
  (`scripts/check-yaak-secrets.sh`); the desktop app's own git sync
  remains the operator's step.
