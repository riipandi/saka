# Frontend integration roadmap

Captured 2026-10-09, after the frontend-foundation plan (`plan-20261009_0025.md`) and the
OAuth-SSO plan (`plan-20261009_0142.md`) closed. This document is an analysis and a
recommended work order, not a plan contract: it names what the backend already serves, what
the webapp already consumes, and the order the remaining wiring should land in. Each wave
here becomes one plan (or a small set of them) when the owner picks it up — until then the
endpoint reference (`.llms/endpoint-reference.md`) stays the contract of record and this
document only points at it.

Sources read for this document: the full endpoint reference, `plan-20261009_0025.md` and
`plan-20261009_0142.md` (the webapp data-layer decisions D1–D10 and the SSO flow D1–D9),
`api/connect/*.proto` service inventory, and the webapp tree (`routes/`, `hooks/`,
`libraries/`) as of 2026-10-09; on 2026-10-10 the Yaak workspace `Saka`
(`wk_kBiMYTkhPP`, 212 requests) was reconciled against the same records (the blockers
section carries what it found). The code remains the only record of what is served —
re-verify a procedure against the tree before wiring it.

## Reference map — the inspirations each wave consults

`NOTICE.md` and `docs/acknowledgements.md` record who Saka learned from. Before
designing or wiring a flow, an agent reads the section of the acknowledged source that
owns the behavior — the wire is already settled by the backend; the reference informs
the flow's shape, the screen's semantics, and the copy:

| Roadmap concern | Reference to consult | What to take from it |
| --- | --- | --- |
| Passkey sign-in/management, the OIDC provider's interaction (consent, device, end-session), SCIM, the audit vocabulary, the settings and email shapes | **Pocket ID** — behavior and screens (`docs/acknowledgements.md`); the ported REST surface (`.llms/references/pocket-id-api-reference.md`); every deliberate departure (`.llms/upstream-deviations.md`) | Flow and screen semantics; where saka already deviates, the deviation wins |
| The sign-in flow semantics **beyond passkey** — sign-in / linking / creation as one flow with its pauses (verified email, missing names), the second-factor bridge, the MFA enrollment fork, the OAuth SSO stages, the configuration-driven surfaces | **Clerk** — the documentation and product behavior (no code) | The stage machine and its copy patterns; the store-driven configuration the connection CRUD mirrors |
| Storage and the resumable upload | **tus 1.0.0 spec + tusd's approach** — the protocol saka adopted as a specification | The client-side session semantics (`tus-js-client` decision rides this) |
| The queue and scheduler (ops console) | **Backlite** / **River** — the engines' designs | Only to understand what the console renders; the SPA consumes the RPC surface, not the engines |
| The surfaces saka originated — password auth, MFA TOTP, one-time access, the session lifecycle, API keys, webhooks, notifications, authorization, device login | **Saka's own records** — `.llms/auth-design.md` first, then the endpoint reference and the code | No external upstream exists; the design documents are the reference |

Two standing rules ride this map:

- **No code copying from the AGPL source.** Pocket ID is AGPL-3.0 — saka reimplements
  behaviors on its own architecture. A frontend wave reads its behavior and screens for
  parity of *experience*, never lifts its source; when the SPA's shape departs, the
  departure belongs in `.llms/upstream-deviations.md`.
- **Docs sync keeps the acknowledgements honest.** A wave whose shipped behavior leans
  on a reference in a way `docs/acknowledgements.md` does not yet name updates that
  table in the same change, per the standing docs-sync rule.

### Types: proto first, zod at the edges

The ConnectRPC codegen is the type system's source of truth — every request, response,
and view the SPA handles already has its TypeScript shape in `~/codegen/*_pb`, and the
snake_case codec is pinned by the transport's tests. A wave adds **no hand-written
interface, type, or schema that re-describes the wire**: import the proto-derived type
(`SignInRequest`, `AuthenticatedUser`, the service messages) and build projections from
it where a view needs a narrower shape.

Zod stays, but only where a runtime validator earns its keep:

- **Untrusted boundaries** — the REST envelope (`/api/configuration` and the other
  `fetcher` responses), the cookie jar, the BroadcastChannel sync message: shapes the
  codegen does not own, parsed before they enter the store.
- **Request/input validation where it is sensible** — a form's or mutation's input
  schema (field constraints, cross-field rules, the settings-form shapes) may be zod,
  when the declarative schema beats hand-rolled validators. The login route's inline
  TanStack Form validators (D2) remain the counter-example: two fields earned no schema.

The rule of record lives in `.llms/rules.md` (the Webapp frontend row, from
`plan-20261009_0025.md` D2); this reminder restates it so a wave never has to rediscover
the boundary. When in doubt: if the shape travels to or from the server over `/rpc`, the
codegen owns it; if it crosses a boundary the codegen cannot see, zod may parse it.

### Configuration: every feature respects the backend's document

The backend owns two configuration surfaces, and a frontend feature never decides a
capability on its own — it reads the document the deployment serves:

- **The deployment document** — `GET /api/configuration` (`useAppConfig`): immutable
  per process, public subset anonymous, the full non-secret document to an
  administrator. It answers whether a *surface exists at all* (`oauth.enabled`,
  `oidc.enabled`, the two one-time-access toggles, the announcement-email switch) and
  the deployment facts (`app.mode`, `base_url`, `timezone`, `assets_url`). D7 is its
  record: refetched when the auth state flips, because the administrator scope widens
  it.
- **The product settings** — `SettingsService/ListPublic` (the Wave 3
  `usePublicSettings` hook): DB-backed, runtime-mutable, the catalog items flagged
  `public`. It answers the product behavior a screen shapes itself around:
  `access.mode` (open vs invite), the allowlist toggle, the signup form-field toggles,
  `mfa.required`, the passkey enrollment limits, the password-policy fields.

The standing rules a wave inherits:

1. **No hard-coded capability.** A toggle the backend publishes is read from the store,
   never spelled in a component. A surface the configuration disables is hidden — and
   the same refusal the server answers (`permission_denied`, the master-switch's
   identical-shape refusal) is what the UI shows if a stale document let a request
   through: the server is the authority, the document is the rendering hint.
2. **Two caches, two lifetimes.** The deployment document is `staleTime: Infinity`
   (the process config changes only at restart) with the auth-flip invalidation; the
   public settings are runtime-mutable, so their cache carries a bounded freshness —
   a settings write in another session lands on the next fetch, not the next deploy.
3. **Render follows the document, never the reverse.** The login screen's SSO block
   under `oauth.enabled` is the pattern: the feature asks the store what it may offer,
   builds its form from the settings' shape (`access.mode`, the signup field toggles),
   and does not invent a middle state.
4. **New capability keys ride their wave.** When a wave's feature needs a public fact
   the document does not carry, the wave's plan adds it to the public scope (the
   `Published` struct, with the publisher test pinning it) in the same change — a
   frontend reading a fact the backend does not publish is a contract violation, not
   a convenience.

## Where the frontend stands

Wired and proven (the foundation plans' output):

- The guard stack (`libraries/guard/`): Comlink worker engine over
  `AuthService/SignIn`, `SessionService/GetSession`, `Refresh`, `SignOut`; cookie
  persistence through the cookies library; tab sync; dead-worker recovery; the auth-aware
  `authFetch` seam under both the REST `fetcher` and the Connect transport;
  `TransportProvider` + Connect-Query; TanStack Query as the front-facing layer.
- `useAppConfig` (`GET /api/configuration`, envelope-validated, refetch on auth flip).
- The login route: password sign-in with `remember`, redirect notices, the SSO block
  (`useOAuthProviders` → `ListEnabledConnections`, begin via `/oauth/{provider}/start`)
  and the `/auth/callback` completion route (`ContinueSignIn`, error-word mapping).
- E2E: the session-foundation flow (release) and the passkey ladder (debug build).

Known gaps inside the already-wired flow:

- An account keeping a confirmed TOTP factor gets `mfa_required` from `SignIn` and the
  frontend refuses it loudly — the challenge is unimplemented by design (`plan-20261009_0025.md`
  out-of-scope note). Same for the enrollment fork (`mfa_enrollment_required` +
  `pending_token`). Any MFA-enabled account cannot sign in through the SPA today.
- `routes/(app)/settings.tsx` is still the DummyJSON demo stub — the profile it renders is
  not the account view the backend serves.
- `routes/(auth)/forgot-password.tsx` is a template stub; `ForgotPassword`/
  `ResetPassword` are shipped and unreached.

## What the backend serves, grouped by the frontend capability it feeds

Every row below is `done`/`shipped` in the endpoint reference unless marked. Grouped by the
user-visible capability, with the wire facts the wiring must honor.

**Session and account self-service** — `GetCurrentUser`, `UpdateCurrentUser`,
`DeleteMyAccount` (refused while `users.self_delete_enabled` is off), the session lifecycle
(`ListSessions`, `RevokeSession`, `SignOutOtherSessions`, `SignOutAllSessions`), profile
picture (`PUT/DELETE /api/users/{id}/profile-picture` raw body, magic-byte sniffed,
2 MiB; public `GET /api/users/{id}/profile-picture.png`). Self-service procedures refuse
machine credentials and impersonated callers.

**Email identity** — `EmailVerificationService` (`SendEmail`, `VerifyEmail`,
`RequestEmailChange`); the verify token travels by email and the SPA needs the route that
consumes it.

**Sign-in completeness** — `MultifactorService` (the `CompleteSignIn` bridge, the
enrollment pair, the settings-page management procedures, recovery codes), the
`PasswordRecoveryService` trio, `OneTimeAccessService` (`RequestEmail` answers a
16-character device token the exchange demands back; the email links to
`/login-code?code=…`; both email asks are toggle-gated in the public configuration),
`WebAuthnService` (discoverable sign-in `BeginLogin`/`VerifyLogin` — full authentication in
one step, no MFA bridge; enrollment and credential management behind `Session`; the
`credential` field is a **string** of browser JSON, never an object), and the shared step-up
proof (`Reauthenticate` + `SendReauthenticationCode`, spent through the
`X-Saka-Reauthentication` header by `AddPassword`, `RemovePassword`,
`DeleteCredential`, `UnlinkConnection`).

**Identity administration** — `UserService` CRUD + `BanUser`/`UnbanUser`/`UnlockUser`,
`ImpersonateUser`/`StopImpersonating` (delegated session; self-service procedures refuse
the delegated caller, so the UI needs the impersonation banner), `UserGroupService`
(including `SetAllowedOidcClients` and the per-account membership reads),
`BlocklistService`, `SignupService` (the public `Signup` rides `access.mode` from the
configuration; the token CRUD is admin), `AuditLogService` (own list `Authenticated`, the
admin trio + `FilterOptions`), `AuthorizationService` (roles, permissions, per-user grants —
the backend half of D10's frontend authorization).

**Federation administration** — `OidcClientService` (CRUD, secrets show-once, logo
upload/delete, `PreviewClient`, `UpdateAllowedUserGroups`, CIMD `RefreshClient`),
`OidcConsentService` (the user's authorized-client ledger + `ListMyClients`, the admin
views), `CustomClaimService` (user and group claims, reserved-key refusals),
`ScimProviderService` (one provider per client, token show-once), the OAuth SSO admin CRUD
and the account-side `ListLinkedConnections`/`UnlinkConnection`/`GetLinkedAccountTokens`.
The client form owes four parked `TODO(frontend)` items recorded in the endpoint reference:
back-channel logout fields, the `allowed_grant_types` editor, the consent screen's
`offline_access` wording, and the `login_hint` prefill.

**Platform administration** — `SettingsService` (`List`/`Update`/`Reset`, `ListPublic`
anonymous), `AppConfigService/TestEmail`, the admin document of `GET /api/configuration`,
`NotificationService` (admin publish/cancel + the user inbox and **`WatchNotifications`
server-streaming** — the first streaming consumer settles the armed D4 fallback clause),
`WebhookService` (ten procedures, secret show-once, delivery history), `ApiKeyService`
(create/list/renew/revoke self-service, `ListAllAPIKeys` admin).

**Device and ops surfaces** — `DeviceApprovalService` (`Inspect`/`Decide`, guard `Session`,
impersonation refused — the SPA renders the approval side; the device side is REST),
`QueueService`/`SchedulerService` (the operations console, all `Admin`), `HealthService`.

**Not wireable yet** — `VersionService` and the `ApiService` surface are `planned` (no
proto); setup endpoints and LDAP are `excluded`. The storage surface (`BucketService` in
`api/connect/storage.proto`, `modules/storage`) has no section in the endpoint reference —
see `issue-20261009_2356.md` before planning that wiring.

## Ordering principles

1. **Fix the broken path before adding paths.** The MFA refusal sits inside the already
   shipped sign-in flow; it is a correctness gap, not a feature.
2. **Self-service before administration.** The account pages are what every user hits and
   what the admin console's user actions link back to; they also exercise the mutation
   patterns (Connect-Query mutations, envelope errors, file upload over the REST seam) on
   procedures with no admin guard.
3. **Build shared machinery with the first wave that needs it, not before.** Step-up, the
   pagination envelope, and the error mapping each land with their first consumer and
   generalize from there. The one exception is deliberate: the authz foundation (Wave
   1.5) lands ahead of its consumers so the admin wave builds screens, not plumbing.
4. **Every wave ends demo-free.** A wave that touches a page removes that page's demo stub
   and its DummyJSON copy; the settings page's warning alert is the pattern to erase.
5. **Gate on the backend's configuration.** The deployment document and the public
   settings decide what a surface offers — features read the stores, never hard-code a
   capability. The full contract is the "Configuration" section above.

## Work order (checklist)

Status vocabulary: an unchecked box is wiring not yet delivered; a checked
box is delivered and validated — the wave's plan has run its composition
probe and test matrix green. Tick a box in the same change that delivers
it, and record the evidence (plan or handover reference) the way the
endpoint reference keeps its Evidence column honest. A wave's heading
carries its rationale; the endpoint reference stays the contract each box
wires against.

### Wave 0 — done (record)

- [x] Session foundation: the guard stack, the cookies library, the
  `authFetch` seam, the Connect-Query transport (`plan-20261009_0025.md`)
- [x] Password sign-in with `remember` on the login route, redirect notices
- [x] OAuth SSO begin + the `/auth/callback` completion route
  (`plan-20261009_0142.md`)
- [x] The backend-driven configuration layer (`useAppConfig`)

### Wave 1 — the account works on real data (self-service)

- [ ] **Profile & preferences** — `GetCurrentUser`/`UpdateCurrentUser`, the
  profile-picture PUT/DELETE (raw body through the REST seam),
  `DeleteMyAccount` behind the toggle. Destubs `settings.tsx`; the account
  view the guard carries gets its authoritative source. First
  Connect-Query mutation consumer and first binary upload.
- [ ] **Session center** — `ListSessions`, `SignOutOtherSessions`,
  `SignOutAllSessions` (the engine already owns `GetSession`/`SignOut`).
  The `unauthenticated`-on-ended-session semantics are already the engine's
  restore signal. **The per-row revoke moved to Wave 2** (owner decision,
  2026-10-10): `RevokeSession` is step-up-guarded
  (`internal/guard/rules.go` marks it `StepUp: true`), and the step-up
  modal this page's action becomes the first consumer of lands there —
  shipping the button now would expose an action that always answers
  `authentication required`.
- [ ] **Email verification & change** —
  `SendEmail`/`VerifyEmail`/`RequestEmailChange`/`ConfirmEmailChange`
  (the change flow's completion half — shipped, see `issue-20261010_0017.md`
  ISS-001 for the reference gap) plus the routes the mailed tokens land on:
  the verify route and the change-confirm route.
- [ ] **Audit trail (own)** — `AuditLogService/List` on the account page;
  the smallest list-with-pagination consumer. Build the shared pagination
  hook (page/limit + `ListMetadata`, `sort_by` whitelists) here.

### Wave 1.5 — the authz foundation (owner decisions, 2026-10-09)

The owner moved the frontend authorization half of D10 ahead of the admin wave. The three
decisions, settled in the same discussion:

- **Grants source — decode the access token's claims.** `jwtutils.AccessClaims` carries
  `roles` (role slugs) and `permissions` (effective grants — the roles' sets plus the
  direct grants) as a mint-time snapshot, and the refresh re-reads the account, so the
  claims stay in lockstep with the bearer the calls actually send. The worker engine
  decodes the payload at custody change and the store carries them beside the profile —
  no backend change, no extra round trip, no RPC for "my grants" (none exists: every
  `AuthorizationService` procedure is `Admin`-guarded).
- **Engine — homegrown, fitted to this project's model.** The backend's authorization is
  the Unkey model (flat permission slugs, roles as named sets, subject = direct grants +
  roles) adapted into `framework/authz` + `internal/authz`; the frontend mirrors that
  binding rather than adopting a library. CASL was the owner's first reference and is
  explicitly not adopted — the model here is flatter than CASL's subjects/conditions.
- **Matching semantics — the `framework/authz` matcher, ported.** Slug
  `resource:instance:action`, the wildcard `*` allowed in the instance position only
  (`internal/guard/guard.go:247` is the behavior being mirrored). A grant
  `user:*:read` satisfies `user:usr_123:read`; nothing else wildcards. The UI and the
  server judge by the same rule, and a requirement written for the guard reads as a
  literal in the UI.

- [ ] **Claims into the store** — the engine (worker) decodes the access token's
  `roles`/`permissions` at custody change; the store carries them beside the profile;
  every refresh replaces the snapshot; sign-out clears them with the pair.
- [ ] **The permission engine** — the TS port of the matcher described above, exposed as
  a `can(requirement)` primitive over the store's grants; unit tests cross-checked
  against the Go matcher's behavior (the same grant/requirement pairs, the same answers).
- [ ] **React ergonomics** — `usePermissions()` and a `<Can permission="user:list">`
  component in `libraries/guard/` (the namespace stays generic per D10 — no `admin`-only
  vocabulary in the shared names).
- [ ] **Administrator signal** — `isAdministrator` derived from the claims (holding the
  `administrator` system role), ready for `admin/route.tsx` to consume in Wave 3.
- [ ] **Refusal mapping** — the Connect codes the guard answers
  (`permission_required`, `permission_denied`) join the error mapping, so a UI check
  that missed a case degrades to the server's word, never a silent lie.
- [ ] **Catalog left out** — `ListPermissions` (the code-declared catalog) is an
  admin-read surface; the screens that render it land in Wave 3. The foundation checks
  against the snapshot alone.

### Wave 1.6 — frontend observability (owner direction, 2026-10-10)

The owner wants OpenTelemetry in the frontend, Pocket ID's frontend package set being
the reference (`@opentelemetry/api`, `sdk-trace-web`, `resources`,
`semantic-conventions`, `exporter-trace-otlp-http`). The backend already dials one
collector over OTLP/HTTP, opt-in per signal (`internal/config/types.go`, the `OTEL`
section); the frontend joins the same story rather than inventing an APM-shaped one.

Reference read 2026-10-10: the damikun workshop article *"Export request traces from
React SPA to backend OpenTelemetry collector"* (`dev.to/damikun/…-4kb4`) — the same
goal, three deltas from its shape:

- **Adopted from it**: the provider-component mounting (`TraceProvider` in the root
  stack, saka mounts it in `__root.tsx` beside the other providers); the exporter's
  `ignoreUrls` posture — saka's version excludes the telemetry endpoint itself and any
  third-party origin, so the exporter never traces its own exports; the
  resource/attribute shape its example wire payload shows; and its "collector behind a
  proxy" option, which is one of the two answers to the endpoint decision below.
- **Rejected from it**: `FetchInstrumentation` (it monkey-patches `window.fetch`; the
  `authFetch` seam is the instrumentation point and both clients ride it) and
  `ZoneContextManager` (the zone-based context manager exists for auto-instrumentation
  callbacks; the seam manages context explicitly). Its `SimpleSpanProcessor` is a
  demo shortcut — saka batches, the way the backend's queue keeps export off the
  request path.

- [ ] **The web tracer** — `WebTracerProvider` with a resource naming the SPA
  (`service.name` the app identifier + `.web`, the deployment's `environment`), the
  OTLP/HTTP trace exporter, and lazy initialization after first paint so the SDK's
  weight never lands on first load.
- [ ] **Spans at the seam** — `authFetch` opens one client span per request (the HTTP
  client semantic conventions), injects `traceparent`, and records the refusal as the
  span's status — one instrumentation point serving both the REST `fetcher` and the
  Connect transport, the same reason the seam exists. No
  `instrumentation-fetch`/`instrumentation-xml-http-request` monkey-patching, no
  `context-zone`.
- [ ] **Navigation spans** — a router span per navigation from TanStack Router's
  lifecycle hooks, naming the route id.
- [ ] **Redaction before export** — URL attributes drop query strings: the reset
  token, the one-time-access code, and the OAuth flow token travel in search params,
  and none of them may reach a collector. The redaction is written once at the
  exporter/processor seam, not per-span.
- [ ] **The worker joins the trace** — the engine's transport injects the trace
  context the main thread passes it, so the auth RPCs are children of the span that
  caused them; the worker never instruments independently.
- [ ] **The enable switch and sampler** — frontend tracing is opt-in like every
  backend signal: it follows the configuration document, and a sampling ratio rides
  with it. The decision the wave's plan settles: the public configuration gains a
  browser telemetry field (the deployment exposes its collector to browsers, or
  explicitly chooses not to), or the endpoint arrives at build time — the first
  keeps the capability source in one place, the second avoids a backend change but
  splits the source of truth.
- [ ] **Metrics and logs stay backend-only for now** — the browser contributes
  traces; console errors and client metrics wait for a named need.

### Wave 2 — sign-in completeness

- [ ] **MFA challenge & enrollment** — the sign-in fork: `CompleteSignIn`
  on `mfa_required`; the enrollment fork's `pending_token` into
  `BeginTotpEnrollment`/`ConfirmTotpEnrollment`. Then the management set
  (`ListTotpEnrollments`, `DeleteTotpEnrollment`, `VerifyRecoveryCode`,
  `RegenerateRecoveryCodes`, `DisableMfa`). This unblocks every
  MFA-enabled account the backend already serves.
- [ ] **Password recovery** — destub `forgot-password.tsx`
  (`ForgotPassword`), add the reset route consuming the emailed token
  (`ResetPassword` with `terminate_sessions`),
  `AdminResetUserPassword` rides the admin wave's user page.
- [ ] **One-time access** — `RequestEmail` (+ device-token state) and the
  `/login-code` route the email links to, `ExchangeToken` issuing the pair
  into the existing custody path. Gated by the two public toggles
  `useAppConfig` already carries; admin issuance rides the admin wave.
- [ ] **Passkeys & step-up** — discoverable sign-in
  (`BeginLogin`/`VerifyLogin`), enrollment and credential management
  (`BeginRegistration`/`VerifyRegistration`,
  `ListCredentials`/`UpdateCredential`/`DeleteCredential`),
  `AddPassword`/`RemovePassword`, and the **step-up machinery** built here
  as shared infrastructure: the `Reauthenticate` modal (password / passkey
  / email-code factors), the `X-Saka-Reauthentication` header plumbing in
  `authFetch`, and the single-use-token lifetime handling. Every later
  destructive action reuses it — **the session center's per-row
  `RevokeSession` is its first consumer** (owner decision, 2026-10-10:
  the action is wired and confirmed in `account/sessions.tsx` the moment
  the modal exists, since the procedure is step-up-guarded). The E2E
  passkey ladder and the debug simulation pages are the harness this wave
  graduates to the real UI.

### Wave 3 — the admin console core

- [ ] **Admin shell & route guard** — the admin layout, the route-level
  guard (profile's administrator status today; `AuthorizationService`-backed
  permission checks when D10's frontend half lands), impersonation banner
  + `StopImpersonating`.
- [ ] **Users** — `ListUsers`/`GetUser`/`CreateUser`/`UpdateUser`/
  `DeleteUser`, `BanUser`/`UnbanUser`/`UnlockUser`,
  `AdminResetUserPassword`, the admin passkey roll
  (`AdminListCredentials`/`AdminUpdateCredential`/`AdminDeleteCredential`),
  `AdminDisableMfa`, `ImpersonateUser`. The pagination hook generalizes.
- [ ] **Groups, blocklist, signup** — `UserGroupService` (members and
  allowed-clients replaces), `BlocklistService`, the signup-token CRUD and
  the public signup page. The signup page's shape is decided by the
  **public settings**, not the configuration document: `access.mode`
  (open vs invite), the allowlist toggle, and the form-field toggles
  (email/username/password, verification-at-signup) are all `Public: true`
  catalog items served by `SettingsService/ListPublic` — the foundation
  plan deferred that surface to "the phase that needs it", and this is
  the phase: it lands the `usePublicSettings` hook beside `useAppConfig`
  (a different document, a different cache entry).
- [ ] **Audit (admin)** — `ListAll`, `ListForUser`, `FilterOptions` facets.
- [ ] **Authorization** — the `AuthorizationService` screens (roles, permissions,
  per-user grants) built **on the Wave 1.5 foundation**: the screens consume the claims
  store and the `can()` engine; permission-aware UI guards (route sections and nav items
  judged by the catalog slugs) land here as the foundation's first real consumers. The
  claims snapshot only refreshes at sign-in/renewal — the screens' writes note that the
  caller's own grants move at the next token, not in place.

### Wave 4 — federation administration

- [ ] **OIDC clients** — CRUD, secrets (show-once patterns), logo upload,
  allowed groups, `PreviewClient`, `RefreshClient`; the four parked
  `TODO(frontend)` items land here.
- [ ] **Custom claims & SCIM** — `CustomClaimService` (user + group claims,
  the reserved-key refusal surfaced as form validation),
  `ScimProviderService`.
- [ ] **OAuth SSO admin** — the connection CRUD (builtin vs custom kinds),
  and the account-side linked-connections page (`ListLinkedConnections`,
  `UnlinkConnection` behind the wave-2 step-up, `GetLinkedAccountTokens`).
- [ ] **Consent surfaces** — the user's authorized clients
  (`ListMyAuthorizedClients`, `RevokeMyAuthorizedClient`,
  `ListMyClients`) and the admin ledger views.

### Wave 5 — platform administration

- [ ] **Settings** — the `SettingsService` CRUD screens (sealed items
  render their `enc:` state, never a value), `TestEmail`, the admin
  configuration document.
- [ ] **Notifications** — admin publish/cancel/list + the user inbox
  (`ListNotifications`, `MarkNotificationRead`, `MarkAllNotificationsRead`,
  `UnreadCount`) and **`WatchNotifications`** — the streaming consumer
  that settles D4's fallback clause; plan it with that decision's terms in
  hand.
- [ ] **Webhooks** — the ten-procedure surface; the show-once secret and
  rotate flows reuse the OIDC-secret UI pattern.
- [ ] **API keys** — self-service CRUD (`CreateAPIKey` show-once, renew,
  revoke) + the admin `ListAllAPIKeys`. The machine-credential refusal on
  self-service surfaces is backend behavior the UI never needs to route
  around.

### Wave 6 — device, ops, storage

- [ ] **Device login approval** — `Inspect`/`Decide` rendered for the
  signed-in holder; the device side is REST and needs no SPA work beyond
  documenting the flow.
- [ ] **Ops console** — `QueueService`/`SchedulerService` screens and
  health reads; the pagination and admin-shell patterns carry it.
- [ ] **Storage** — blocked on the endpoint-reference gap (see
  `issue-20261009_2356.md`); plan it only after the shipped storage
  surface is inventoried and the tus/REST split is on the reference's
  books.

## Shared machinery (build with the wave marked)

- [ ] Pagination hook (`page`/`limit` + `ListMetadata`, sort whitelists) —
  lands with Wave 1 (audit list); every admin list reuses it.
- [ ] Connect error → message mapping (`already_exists`,
  `failed_precondition`, `resource_exhausted` cooldowns,
  `permission_denied`) — lands with Wave 1 (profile update); extends
  `guard/auth-utils`.
- [ ] File upload over the REST seam (raw body) — lands with Wave 1
  (profile picture); reused by OIDC logos.
- [ ] Step-up modal + `X-Saka-Reauthentication` in `authFetch` — lands
  with Wave 2 (passkeys/password); shared by `DeleteCredential`,
  `RemovePassword`, `UnlinkConnection`.
- [ ] Permission engine (`can()` over the claims snapshot, the `framework/authz`
  matcher ported) + `usePermissions()`/`<Can>` — lands with Wave 1.5; every later
  permission-aware UI rides it.
- [ ] Admin route guard + admin shell + impersonation banner — guard prototype with
  Wave 1.5 (consumes `isAdministrator`), the shell lands with Wave 3; permissions gate
  it when D10's screens land.
- [ ] Show-once secret presentation pattern — lands with Wave 4 (OIDC
  secrets); reused by webhooks, SCIM, API keys, signup tokens.
- [ ] Streaming consumer (`WatchNotifications`) — lands with Wave 5;
  settles the D4 fallback clause.
- [ ] Public-settings hook (`usePublicSettings` over
  `SettingsService/ListPublic`) — lands with Wave 3 (the signup page);
  the deployment document and the product settings are separate surfaces.

## Blockers and dependencies (Yaak reconciliation, 2026-10-10)

The Yaak workspace `Saka` (`wk_kBiMYTkhPP`) holds 212 requests. The
reconciliation against the endpoint reference, the protos, the guard
rules, and the configuration publisher found no missing backend surface —
every shipped procedure the roadmap wires exists and is guarded — but
these defects and dependencies sit in the wiring path:

**Cross-check `.llms/issues/` before working any of them.** This list is
a snapshot, not the tracker: the issues directory is. Before a wave's
plan touches a blocker — or any surface these items name — read the
newest capture files for `open` and `considering` items on the same
area: one may carry the decision, the reopen trigger, or the prior
attempt that changes the work. Conversely, any finding a wave surfaces
that outlives its turn lands in a new capture file per the standing
Issues rule in `AGENTS.md`; the item resolved here gets its status
updated in the file it lives in, never only in this snapshot.

1. **Storage has no contract of record** — the collection carries ten
   storage requests (five `BucketService` RPC procedures, five tus
   REST routes) while the endpoint reference carries zero rows. Wave 6's
   storage item stays blocked until the inventory lands (see
   `issue-20261009_2356.md`).
2. **`ConfirmEmailChange` is absent from the endpoint reference** —
   shipped (proto, guard `Public`, Yaak request present) but
   undocumented; Wave 1's email item now names it and waits on
   `issue-20261010_0017.md` ISS-001 for the reference row.
3. **The Yaak collection's password-recovery rows are stale** —
   "Enroll password recovery" and "Finalize password recovery" name no
   saka procedure, and the three real ones (`ForgotPassword`,
   `ResetPassword`, `AdminResetUserPassword`) have no requests at all:
   Wave 2's recovery work has no collection coverage (ISS-002).
4. **`GetLinkedAccountTokens` has no Yaak request** — shipped, guarded,
   documented, but the collection omits it; the Wave 4 linked-connections
   page lacks its checklist row (ISS-003).
5. **The streaming clause is still armed** — `WatchNotifications` is the
   only server-streaming procedure, and the `ofetch.raw` body-read defect
   that bit the SSO plan's probe (plan-20261009_0142, phase 2) is exactly
   the class of bug a streaming consumer re-risks. Wave 5 plans the
   notifications work with a streaming probe before any UI.
6. **The MFA fork is the sign-in blocker** — already recorded above, and
   the reconciliation confirms it is the only shipped procedure the
   wired login flow refuses: an MFA-enabled account cannot complete
   sign-in in the SPA until Wave 2's first item lands.
7. **Not blockers, but adjacent**: `VersionService` and the `ApiService`
   surface remain `planned` (no proto, no requests — correctly absent
   from the collection); the setup endpoints are `excluded` and the two
   configuration-write rows in the collection are already marked so.
   The `AssetsURL` comment/code mismatch (`issue-20261010_0017.md`
   ISS-004) stays parked until a split-assets deployment is a real
   decision.
8. **The browser's collector endpoint is undecided** — the backend dials
   one OTLP/HTTP collector, opt-in per signal, but the public
   configuration publishes no address a browser could export to (and the
   collector is often loopback-only). Wave 1.6's plan must settle it:
   a dedicated public field in the configuration document (recommended —
   the capability source stays in one place, empty meaning frontend
   tracing off), a build-time variable (no backend change, split source
   of truth), or the same-origin proxy the damikun reference uses — the
   deployment's reverse proxy answers a fixed path (e.g. `/otel/*`) to
   the collector, the SPA posts to same-origin, and no Go change is
   needed. Until decided, the exporter has nowhere to dial.

## Route structure (recommended file layout)

The routes directory is TanStack Router file-based, generated into `routes.gen.ts` (never
hand-edited). Verified in the tree 2026-10-09:

- `__root.tsx` mounts the providers and reads `staticData.pageTitle`;
  `-boundaries.tsx` and `-devtools.tsx` are shared components — the `-` prefix keeps a
  file out of the route tree.
- `(auth)/route.tsx` is the anonymous-only group: `ensureSessionLoaded()` then redirect
  the authenticated caller to `return_to ?? '/overview'`, prefetching the configuration
  and provider queries while the document renders. Children today: `login.tsx`,
  `forgot-password.tsx` (stub), `auth/callback.tsx` — `/login`, `/forgot-password`,
  `/auth/callback`.
- `(app)/route.tsx` is the authenticated shell: the same session bootstrap, the
  `isAuthenticated` guard redirecting with `return_to` + `unauthenticated`, the sidebar,
  and the eviction effect that bounces a profile that disappears mid-session. Children
  today: `overview.tsx`, `settings.tsx` (DummyJSON stub).
- Template residue rides in the shell: the `index.tsx` hero ("Vite React Template",
  "Demo sign in"), the sidebar's logo ("ReactiVite") and placeholder nav items
  (Search/Analytics/Docs/Products/Messages with `href: undefined`). The de-templating is
  Wave-1 work under the demo-free principle, not a separate pass.

Conventions the waves keep:

1. A `route.tsx` inside a section folder owns that section's layout and guard; page files
   stay flat inside the folder, one per page.
2. Groups never duplicate a shell: the admin area nests **inside** `(app)/` so the
   sidebar and eviction behavior are inherited, and its `route.tsx` adds only the admin
   guard on top. Detail pages are `$id.tsx` files beside their list.
3. Guards ride the bootstrapper first (`ensureSessionLoaded()`), preserve `return_to` in
   the redirect, and carry the notice word (`unauthenticated`, `loggedOut`) the login
   route already renders.
4. Every route sets `staticData.pageTitle`; query state rides `validateSearch` (zod);
   above-the-fold queries prefetch in the section's `route.tsx` loader, the way
   `(auth)/route.tsx` warms the login page's two queries.
5. **Route files stay small through dash-file code splitting** (TanStack Router): the
   route file declares `createFileRoute` — search params, page title, and a thin
   component that wires `useSearch`/`useNavigate` to the view — while the
   implementation lives beside it in a `-`-prefixed file (`-settings-view.tsx`);
   files and folders with the `-` prefix are excluded from the route tree and never
   answer a URL (<https://tanstack.com/router/latest/docs/routing/file-based-routing>).
6. Shared components inside `routes/` keep the `-` prefix; composition reused across
   sections lives under `src/components/` or `src/libraries/`, but every primitive is a
   `uilibs` component — a missing one is added to `packages/uilibs/`, never webapp-local,
   and an existing one's design is never reshaped to fit a feature. Compose screens with
   the skills `emil-design-eng` and `apple-design` in force (owner direction, 2026-10-10),
   and check the component's Storybook story beside it before building on it.

Target layout, annotated with the wave that lands each file:

```text
routes/
  __root.tsx                    # providers + pageTitle — unchanged
  index.tsx                     # landing — destub the hero (Wave 1)
  verify-email.tsx              # token consumption, no guard — works signed-in or not (Wave 1)
  -boundaries.tsx               # shared, not a route
  -devtools.tsx                 # shared, not a route

  (auth)/route.tsx              # anonymous-only guard + prefetches — unchanged
    login.tsx                   # password + SSO + the MFA fork handoff (Wave 0 / Wave 2)
    forgot-password.tsx         # ForgotPassword (Wave 2)
    reset-password.tsx          # ResetPassword; the emailed token in search (Wave 2)
    login-code.tsx              # OneTimeAccess ExchangeToken; ?code=… (Wave 2)
    signup.tsx                  # Signup, access.mode-gated (Wave 3)
    auth/callback.tsx           # OAuth flow landing — shipped (Wave 0)

  (app)/route.tsx               # authenticated shell — unchanged shape; gains the
    |                           #   impersonation banner mount (Wave 3)
    overview.tsx
    settings.tsx                 # profile & preferences — destub (Wave 1)
    -settings-view.tsx           #   the implementation; dash files are not routes
    device.tsx                   # device-login approval: Inspect + Decide (Wave 6)
    account/
      sessions.tsx               # session center (Wave 1)
      -sessions-view.tsx         #   the implementation
      email.tsx                  # verification & change status (Wave 1)
      security.tsx               # passkeys + MFA + password management + step-up (Wave 2)
      audit.tsx                  # own audit trail (Wave 1)
      connections.tsx            # linked OAuth + authorized clients (Wave 4)
      api-keys.tsx               # self-service keys (Wave 5)
      notifications.tsx          # inbox + WatchNotifications (Wave 5)
    admin/
      route.tsx                  # admin guard on top of the (app) shell (Wave 3)
      users/
        index.tsx                # list (Wave 3)
        $id.tsx                  # detail: groups, roles, claims, credentials (Wave 3)
      groups/
        index.tsx                # list (Wave 3)
        $id.tsx                  # detail: members + allowed clients (Wave 3)
      blocklist.tsx              # (Wave 3)
      signup-tokens.tsx          # (Wave 3)
      audit.tsx                  # ListAll + FilterOptions (Wave 3)
      roles.tsx                  # AuthorizationService screens (Wave 3)
      consents.tsx               # admin consent ledger (Wave 4)
      oidc/
        index.tsx                # client list (Wave 4)
        $id.tsx                  # detail: secrets, logo, groups, preview (Wave 4)
      oauth.tsx                  # SSO connections CRUD (Wave 4)
      claims.tsx                 # custom claims (Wave 4)
      scim.tsx                   # providers (Wave 4)
      notifications.tsx          # admin publish/cancel (Wave 5)
      webhooks/
        index.tsx                # endpoints (Wave 5)
        $id.tsx                  # detail + deliveries (Wave 5)
      api-keys.tsx               # ListAllAPIKeys (Wave 5)
      settings.tsx               # SettingsService CRUD (Wave 5)
      queue.tsx                  # ops console (Wave 6)
      scheduler.tsx              # ops console (Wave 6)

  (interaction)/route.tsx       # neither anonymous-only nor the app shell:
    consent.tsx                 #   the OIDC consent interaction — the visitor may be
                                #   signed in or not; posts the decision to
                                #   /oidc/authorize/{id} (Wave 4)
```

Notes the tree does not say:

- The MFA challenge lives **inside** `login.tsx`'s flow, not on a standalone route: the
  sign-in response's fork (`mfa_required` / the enrollment bridge) replaces the card's
  content in place, so the `return_to` context and the pending state never leave the
  route. A standalone `/mfa` route would have to pass the bridge token through search
  params a refresh would drop.
- `(interaction)` exists because the OIDC authorize redirect lands there with whatever
  authentication state the browser carries — neither group's guard applies. Its
  `route.tsx` only ensures the session is loaded, then `consent.tsx` decides: signed in →
  render the consent question; anonymous → hand off to `/login` with `return_to` pointed
  back at the interaction URL.
- `verify-email.tsx` sits at the root for the same reason: the emailed link may reach a
  signed-in user changing their address or an anonymous one confirming a signup.
- Admin navigation appears in the sidebar only when the caller qualifies (administrator
  status now, permission checks when D10's frontend half lands); the placeholder nav
  items leave with Wave 1's shell cleanup.
- The `/auth/callback` path is the backend's contract (`spaCallbackPath`,
  `plan-20261009_0142.md` D4) — the file's location under `(auth)/auth/` is what keeps
  the URL; do not rename it without a backend change in the same commit.

## E2E scenario matrix (happy / unhappy per flow)

The harness is `packages/e2e-tests/` — Playwright, one real Chromium against the
webServer's build (release by default; `E2E_BUILD_TAG=debug` only where a simulation page
is the subject), one serial `test.describe` per flow in `workflow/<flow>.test.ts`, data
seeded over the wire (the `adminToken` → Bearer-CRUD pattern `session-foundation.test.ts`
establishes) and cleaned up in the same act. One file per flow, named for the flow it
proves. Each wave's plan turns its rows below into that wave's test file; this matrix is
the scenario contract, not the code.

Standing harness facts the rows rely on:

- **Mailpit** is the email reader for every flow whose token travels by mail
  (recovery, verification, change, one-time access): the harness requires
  `docker compose -f container/compose.yaml up -d mailpit` and reads the delivery off
  the Mailpit HTTP API (`:8025`) — the same path the Go integration tests prove.
- **The virtual authenticator** (`context.credentials`, the passkey ladder's tool) covers
  every WebAuthn ceremony; a deterministic TOTP code is computed from the enrollment
  secret the begin response answers, so no clock-guessing.
- **The OIDC relying-party side** reuses `packages/e2e-tests/conformance/`'s driver —
  the same client machinery the certification harness drives — for authorize/token
  flows; the browser-side consent acts run in the SPA like any other page.
- **Unhappy paths assert the user-visible refusal**, not the error code — the wording
  the screen shows is the contract a regression would break.

### Wave 0 — session foundation (shipped)

File: `workflow/session-foundation.test.ts` (happy: shipped; unhappy rows below extend it).

- Happy: the login route offers only what the store enables; password sign-in lands in
  the shell; reload restores from cookies before the network answers; sign-out returns
  to the login route.
- Unhappy: a wrong password shows the generic failure (unknown identity and wrong
  password are indistinguishable by design); a signed-in visitor opening `/login` is
  returned to `return_to`; a revoked-elsewhere session's next navigation lands on the
  login route with the `unauthenticated` notice; an SSO flow ending in an error word
  renders its message on `/auth/callback`.

### Wave 1 — account self-service

File: `workflow/account-profile.test.ts`, `workflow/account-sessions.test.ts`,
`workflow/account-email.test.ts`, `workflow/account-audit.test.ts`.

- Profile — happy: the settings page renders the account view (`GetCurrentUser`);
  updating names/locale/timezone persists after reload; a valid PNG upload swaps the
  picture and the public URL changes; reset restores the bundled default. Unhappy: an
  oversized (>2 MiB) and a non-image upload are refused with the field's message;
  `DeleteMyAccount` behind the off toggle refuses; behind the on toggle the deletion
  signs the browser out and the account's identity no longer signs in.
- Sessions — happy: the list marks the current row and orders newest-first; opening a
  second browser context and revoking it from the first evicts the second on its next
  navigation; sign-out-others answers the count and keeps the caller's row. Unhappy: an
  already-ended target is the same quiet success; the caller's own row cannot be
  revoked into a dead shell (the engine's restore path takes over).
- Email — happy: request verification → Mailpit holds the token → the verify route
  flips the state; request email change → confirm code (read from Mailpit) → the
  address swaps and the old address gets its notice. Unhappy: a resend inside the
  cooldown is held with the visible notice; an unknown or spent verify token refuses;
  a wrong confirm code refuses and the address keeps its old value.
- Own audit — happy: the account's records render newest-first; page 2 serves a
  different window. Unhappy: a `sort_by` outside the whitelist falls back without an
  error toast (the server's default), never a broken table.

### Wave 1.5 — authz foundation

File: `workflow/authz-foundation.test.ts`.

- Happy: an administrator sees the admin entry point after sign-in; a seeded
  scenario-role account (the `editor` the user seeder grants) does not; a refresh
  replaces the store's snapshot in lockstep with the new pair. Unhappy: a
  non-administrator opening `/admin/users` directly is turned away by the guard, not
  served an empty table; a UI check that misses is caught by the server's
  `permission_required` word, which the error mapping renders.

### Wave 2 — sign-in completeness

Files: `workflow/mfa-totp.test.ts`, `workflow/password-recovery.test.ts`,
`workflow/one-time-access.test.ts`, `workflow/passkeys.test.ts` (the ladder's graduate),
`workflow/step-up.test.ts`.

- MFA — happy: enroll (begin answers the secret → the computed code confirms → the
  recovery set answers exactly once); a subsequent password sign-in pauses at the
  challenge and `CompleteSignIn` lands in the shell; an enrollment-fork account
  (`mfa_enrollment_required`) rides `pending_token` straight into enrollment. Unhappy:
  three wrong codes exhaust the budget and the bridge dies; a recovery code works once
  and is gone; disabling MFA demands the second-factor proof and refuses without it;
  the challenge's expired bridge returns the visitor to the login route with the
  sign-in again message.
- Password recovery — happy: forgot-password → Mailpit holds the token → the reset
  route swaps the credential → the old sessions are dead (`terminate_sessions`) → the
  new password signs in. Unhappy: an unknown address answers the same success as a
  known one (asserted as identical copy); a weak password is refused by the policy
  words; an unknown, spent, or expired token refuses; a mailer-less run refuses the
  ask loudly.
- One-time access — happy: request email (the toggle on) → the device token pairs with
  the code → `/login-code?code=…` completes → the session's provider names
  `one_time_access`. Unhappy: the toggle off hides the entry and the procedure answers
  `permission_denied` (the same refusal either toggle-off path gives); a mismatched
  device token leaves the code spendable; an expired code refuses; a re-request inside
  the cooldown answers the same success as the first.
- Passkeys — happy: discoverable sign-in issues the session in one step — including on
  an MFA-enabled account, no bridge; enrollment, rename, and the roll render. Unhappy:
  deleting the last credential while no password stands refuses with the
  precondition's message; the cloned-credential refusal (counter rewind, the debug
  harness) ends the ceremony silently by design.
- Step-up — happy: the password proof spends into the guarded call
  (`X-Saka-Reauthentication`); the email-code factor delivers via Mailpit and proves.
  Unhappy: a spent proof token is refused on the second guarded call; a wrong code
  never mints a proof.

### Wave 3 — the admin console core

Files: `workflow/admin-shell.test.ts`, `workflow/admin-users.test.ts`,
`workflow/admin-groups.test.ts`, `workflow/admin-audit.test.ts`,
`workflow/admin-roles.test.ts`, `workflow/signup.test.ts`.

- Shell & impersonation — happy: the administrator reaches the admin screens; the
  impersonation banner names the borrowed account; `StopImpersonating` returns the
  actor's own session. Unhappy: a non-administrator is refused at the guard; an
  impersonated caller's self-service procedure answers the refusal the guard carries
  (the delegated caller cannot delete the account it wears).
- Users — happy: create with groups, update, ban (the ban ends the target's live
  sessions in the same act — assert the target's second context dies), unban,
  unlock-after-lockout; the admin passkey roll renames and removes. Unhappy: deleting
  the signed-in account refuses; banning another administrator is refused at the
  impersonation line; `AdminResetUserPassword` reports the states
  (`unknown`/`banned`/`no address`) distinctly.
- Groups, blocklist, signup — happy: a group's member replace and allowed-clients roll
  persist; the public signup page (open mode from the public settings) creates the
  account and joins the token's groups. Unhappy: a blocklisted address is refused at
  signup (and the block-email-subaddresses rule holds); an invite-mode deployment
  refuses a tokenless signup; an unknown signup token refuses; a duplicate group name
  answers `already_exists`.
- Audit (admin) — happy: `ListAll` filters by event and user; `FilterOptions` feeds the
  facets. Unhappy: a search with no matches renders the empty state, not an error.
- Authorization — happy: a role is created, granted catalog slugs, assigned; the
  assignee's fresh sign-in carries the permission (assert the UI gate flips). Unhappy:
  a slug outside the catalog refuses; a system role refuses update/delete; deleting a
  role accounts still hold refuses.

### Wave 4 — federation administration

Files: `workflow/admin-oidc-clients.test.ts`, `workflow/consent-interaction.test.ts`,
`workflow/admin-sso.test.ts`, `workflow/account-connections.test.ts`,
`workflow/admin-claims.test.ts` (+ SCIM inside it).

- OIDC clients — happy: create (the secret shows once and never again), update,
  secrets add/withdraw, logo upload/delete, allowed groups replace, preview renders the
  claim maps. Unhappy: a CIMD refresh outside the allowlist refuses; a reserved custom
  claim key refuses on create and update.
- Consent interaction — happy (conformance driver as the RP): authorize → the SPA's
  consent question → approve → tokens at the RP; a second authorize with
  `prompt=none` completes silently off the browser-session marker. Unhappy: denying
  consent returns the RP's error redirect; a group-restricted client refuses an
  ineligible account with `access_denied`; an unregistered
  `post_logout_redirect_uri` refuses.
- OAuth SSO admin + linked accounts — happy: the CRUD drives the store; the account
  page lists the caller's bindings. Unhappy: unlinking the last credential of an
  account with no password and no passkey refuses with the precondition's message; the
  begin procedure answers `not_found` for an unknown slug (never a connection
  enumeration).
- Claims & SCIM — happy: a user claim and a group claim round-trip into
  `PreviewClient`'s maps; a SCIM sync pushes to the scripted remote and its counts
  answer. Unhappy: the reserved-key refusal (above) and a claim moved between subject
  kinds refuses; a malformed remote listing fails the pass without deletes
  (asserted over the counts).

### Wave 5 — platform administration

Files: `workflow/admin-settings.test.ts`, `workflow/notifications.test.ts`,
`workflow/admin-webhooks.test.ts`, `workflow/account-api-keys.test.ts`.

- Settings — happy: an update lands and `List` answers the effective value; a reset
  returns the default. Unhappy: a sealed item renders its `enc:` state, never a value;
  an unknown key is refused everywhere.
- Notifications — happy: an admin publish reaches the target's inbox; the unread count
  ticks; mark-read and mark-all clear it; the streaming watch delivers a new
  notification live (the D4-fallback probe rides this act). Unhappy: a notification
  naming an unknown account refuses at publish; a system notice with a topic refuses.
- Webhooks — happy: create (secret once) → the test delivery arrives at the in-test
  receiver with the signature headers valid → the delivery list records the attempt.
  Unhappy: a subscription naming an uncataloged event refuses; a delivery to a
  refusing receiver shows the failed terminal state.
- API keys — happy: create (the raw key shows once) → the key drives an
  administrative read via `X-API-KEY` → revoke → the same read refuses. Unhappy: a
  duplicate name answers `already_exists`; renewing an unexpired key refuses; the key
  cannot touch the surface that manages keys (the guard's rule, asserted once).

### Wave 6 — device, ops, storage

Files: `workflow/device-approval.test.ts`, `workflow/ops-console.test.ts`; storage
waits on its inventory (see Blockers).

- Device approval — happy: the device context creates the request; the signed-in
  account inspects the code as typed (any case, with or without the hyphen) and
  approves; the exchange answers the account view. Unhappy: a decided request is the
  not-found on re-decision; a denied request tells the device; an expired request
  refuses both sides.
- Ops console — happy: queues list with live counts; a seeded dead task replays and
  the count answers; a scheduled job runs now without moving its schedule. Unhappy: an
  unknown or malformed `que_…`/`scd_…` id answers the not-found; cancelling a claimed
  task refuses with the precondition's message.

## Dependency research (supporting libraries)

Checked 2026-10-10 against the owner's criteria: **well-maintained** (current release,
alive upstream), **as minimal as possible** (zero-dependency, single-purpose, no
framework lock-in), and **a fit for the architecture that exists** — the worker custody
model, the `authFetch` seam, Connect-Query as the data layer, and `uilibs` as the
component home. The tree already carries the heavy parts: TanStack (router/query/form/
store), ConnectRPC + connect-query, ofetch, StyleX, zod, cookie-es, Comlink,
`@simplewebauthn/browser`, `@fingerprintjs/fingerprintjs`, `@date-fns/tz`, `blobatar`.

### Adopt (the families, each feature-scoped)

- **`uqr`** (0.1.3, 2026-04, zero dependencies) — Wave 2, MFA enrollment: renders the
  `otpauth://` URI the begin procedure answers as an SVG QR the authenticator app
  scans. Pure string-in/markup-out, tree-shakeable, no canvas — it fits a StyleX
  component. Rejected: `qrcode` (node/canvas-shaped API), `react-qr-code` (a React
  wrapper where a render function is enough), the Barcode Detector API (detection, not
  generation, and uneven support).
- **`otpauth`** (9.5.2, 2026-09) — a **devDependency of `packages/e2e-tests` only**:
  computes the deterministic TOTP codes the MFA acts enter, from the secret the begin
  response answers. Never ships to the bundle. Rejected: hand-rolled HMAC-SHA1 math in
  the test (correct but opaque — the scenario proves the flow, not the algorithm).
- **The OpenTelemetry web family** (Wave 1.6) — the five packages Pocket ID's frontend
  carries: `@opentelemetry/api`, `@opentelemetry/sdk-trace-web`,
  `@opentelemetry/resources`, `@opentelemetry/semantic-conventions`, and
  `@opentelemetry/exporter-trace-otlp-http`. The five-package shape is the
  OpenTelemetry project's own packaging; this set is the minimal one that yields a
  tracing provider. Rejected: the auto-instrumentation packages
  (`instrumentation-fetch`, `instrumentation-xml-http-request`, `context-zone`) —
  they monkey-patch globals where the `authFetch` seam is the single honest
  instrumentation point; Sentry/APM SaaS SDKs — a hosted dependency where the
  deployment already owns its collector; `@opentelemetry/sdk-metrics` and the logs
  SDK — the browser ships traces only until a named need.

### Defer with a named trigger

- **`tus-js-client`** (4.3.1, 2026-09) — Wave 6, storage: the one candidate a wave may
  genuinely need and the only one whose necessity is unproven until the storage
  inventory lands (`issue-20261009_2356.md`). The profile picture already rides the
  REST raw-body seam; if the inventory confirms SPA-facing tus sessions, the client
  rides `authFetch` (D4's seam) with the protocol's own headers, not a second HTTP
  engine. Trigger: the storage inventory's verdict.

### No new dependency — the gaps a wave could be tempted to fill

Recorded so a future agent does not reach for a package where the tree already answers:

- **JWT claims decode** (Wave 1.5) — base64url + `JSON.parse` in the worker. A JWT
  library would ship signature verification the client must never do (the backend
  verifies); the decode is a convenience read of the token the frontend already holds.
  `jose` was evaluated (6.2.12, 2026-09 — well-maintained, zero-dependency) and its
  `decodeJwt` is exactly the needed shape, but adopting a JOSE library for one decode
  fails the minimality bar; revisit only when a browser-side JOSE need is real
  (client-side verification, JWK inspection), none of which the roadmap names.
- **Server-streaming** (Wave 5) — connect-web's transport carries it; no EventSource,
  no polyfill, no SSE lib.
- **UI components, comprehensively** — `packages/uilibs/` is the component home and it
  is extensive: the base set (button, input, field, form, checkbox, select, combobox,
  dialog, drawer, dropdown-menu, popover, tabs, toast, tooltip, otp-field, …) and the
  extra set (alert, badge, data-grid, pagination, table, file-upload, calendar, command,
  empty, sheet, skeleton, sortable, kanban, …). A wave **composes screens from uilibs**
  — it never hand-rolls a control the package carries and never adds a component
  dependency for one. `uilibs` is the shared primitive layer, and its existing
  components' designs are **not altered to fit a feature** — a restyle there ripples
  into every consumer. A genuinely missing primitive may be **added** to uilibs (new
  component, Storybook story included); reshaping an existing one is avoided — a
  feature adapts through composition, props, or a feature-local wrapper built on the
  primitive. Only page-level composition (a screen's own arrangement of uilibs parts)
  lives in the webapp.
- **Tables, pagination, OTP input, file upload, calendar, toasts** — the uilibs rows
  above cover them (`data-grid`, `pagination`, `otp-field`, `file-upload`, `calendar`,
  `toast`). Nothing added; screens compose.
- **Timezone and locale lists** (Wave 1) — `Intl.supportedValuesOf('timeZone')` and
  `Intl.DisplayNames`; `@date-fns/tz` covers the conversions.
- **Passkey ceremonies** (Wave 2) — `@simplewebauthn/browser` is already the
  dependency; the opaque-JSON contract is what it produces and consumes.
- **Device token** (Wave 2, one-time access) — `@fingerprintjs/fingerprintjs` is
  already in the tree (`libraries/device-fingerprint.ts`).
- **Password strength meter** — the server's policy is the contract; the form renders
  the public-settings rule checklist (min length, char rules) as plain checks. No
  zxcvbn — a ~800 KB dictionary bundle for a judgment the backend makes anyway.
- **Charts** (ops console, overview) — no chart in the tree and no wave commits to
  one. Trigger: the first screen that actually plots; evaluate then.
- **JSON/diff viewers, cron editors** — render what the server answers (`<pre>` JSON,
  the raw spec beside `next_due`). Nothing added until a screen proves the need.

## Deferred, and why

- **Frontend authorization half of D10** — the foundation moved to Wave 1.5 (owner
  decision, 2026-10-09); what stays deferred is the admin-facing part: the
  `AuthorizationService` screens and the permission-gated UI, which ride Wave 3 where
  the admin shell gives them something real to guard.
- **`check_session_iframe` / front-channel session management** — deferred on the backend
  by a recorded decision; no frontend work until that decision flips.
- **`VersionService`, `ApiService`** — `planned` on the backend; nothing to wire.
- **Storage wiring** — blocked on the reference gap recorded in `issue-20261009_2356.md`.
