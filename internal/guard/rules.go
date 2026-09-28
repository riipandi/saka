package guard

import (
	"net/http"
	"strings"

	apikeyv1 "github.com/riipandi/tango/codegen/proto/go/tango/apikey/v1"
	apikeyv1connect "github.com/riipandi/tango/codegen/proto/go/tango/apikey/v1/apikeyv1connect"
	auditlogv1 "github.com/riipandi/tango/codegen/proto/go/tango/auditlog/v1"
	auditlogv1connect "github.com/riipandi/tango/codegen/proto/go/tango/auditlog/v1/auditlogv1connect"
	authnv1 "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1"
	authnv1connect "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1/authnv1connect"
	authzv1 "github.com/riipandi/tango/codegen/proto/go/tango/authz/v1"
	authzv1connect "github.com/riipandi/tango/codegen/proto/go/tango/authz/v1/authzv1connect"
	federationv1 "github.com/riipandi/tango/codegen/proto/go/tango/federation/v1"
	federationv1connect "github.com/riipandi/tango/codegen/proto/go/tango/federation/v1/federationv1connect"
	identityv1 "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1"
	identityv1connect "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1/identityv1connect"
	notificationv1 "github.com/riipandi/tango/codegen/proto/go/tango/notification/v1"
	notificationv1connect "github.com/riipandi/tango/codegen/proto/go/tango/notification/v1/notificationv1connect"
	settingsv1 "github.com/riipandi/tango/codegen/proto/go/tango/settings/v1"
	settingsv1connect "github.com/riipandi/tango/codegen/proto/go/tango/settings/v1/settingsv1connect"
	systemv1 "github.com/riipandi/tango/codegen/proto/go/tango/system/v1"
	systemv1connect "github.com/riipandi/tango/codegen/proto/go/tango/system/v1/systemv1connect"
	"github.com/riipandi/tango/pkg/jwtutils"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Entry declares the rule one procedure gets, and — for a self rule — the
// request field the account is named in.
//
// Prototype is the request message the field is resolved against. It is kept
// beside the field so the table can be checked against the contract: a field
// that no longer exists fails a test instead of becoming a guard that refuses
// everything.
type Entry struct {
	// Rule decides who may call the procedure.
	Rule Rule
	// Field is the request field a self rule compares against the caller. It
	// is empty for every other rule.
	Field string
	// Prototype is the request message Field belongs to.
	Prototype proto.Message
}

// ProcedureRules is the rule every procedure on the RPC surface gets.
//
// A procedure absent from this map is administrative. That is the default
// because forgetting to declare a new procedure must fail closed: an
// undeclared procedure refuses a caller who is not an administrator, which is
// a bug report, while an undeclared procedure answered openly is a breach
// nobody notices.
//
// The map is keyed by the generated procedure path, so a renamed contract
// breaks the build rather than leaving a rule attached to nothing.
var ProcedureRules = map[string]Entry{
	// The surfaces a caller reaches before they hold a token, or that a
	// monitor probes without one.
	systemv1connect.HealthServiceCheckProcedure:                    {Rule: Public},
	authnv1connect.AuthServiceSignInProcedure:                      {Rule: Public},
	identityv1connect.SignupServiceSignupProcedure:                 {Rule: Public},
	identityv1connect.EmailVerificationServiceVerifyEmailProcedure: {Rule: Public},

	// The email-change flow splits at the same line its sibling
	// verification does: the request runs on the caller's own token, and
	// the confirmation is answered without one — the pending token is the
	// credential it judges.
	identityv1connect.EmailVerificationServiceRequestEmailChangeProcedure: {Rule: Authenticated},
	identityv1connect.EmailVerificationServiceConfirmEmailChangeProcedure: {Rule: Public},

	// The one-time access codes. The exchange is the sign-in a caller makes
	// with a code instead of a password, so it is reached before any token
	// exists; the public email ask is reached from the sign-in page for the
	// same reason. The two administrative procedures hand a credential to an
	// account's owner or drive the mailer at one address, which is
	// administrative work on an account.
	authnv1connect.OneTimeAccessServiceExchangeTokenProcedure:       {Rule: Public},
	authnv1connect.OneTimeAccessServiceRequestEmailProcedure:        {Rule: Public},
	authnv1connect.OneTimeAccessServiceCreateTokenProcedure:         {Rule: Admin},
	authnv1connect.OneTimeAccessServiceRequestEmailAsAdminProcedure: {Rule: Admin},

	// The multifactor surfaces split at the same line the one-time access
	// codes do. CompleteSignIn is reached with the pending token the
	// password check minted — the caller holds no access token yet, and the
	// pending token is the credential the procedure judges. The enrollment,
	// listing, regeneration, and switch-off run on the caller's own token,
	// so `Session` names them: a machine credential has no second factor to
	// manage, and the refusal of an impersonated caller keeps an
	// administrator from enrolling a factor onto the account they are
	// wearing — the delegation must end before the account's guard changes.
	authnv1connect.MultifactorServiceCompleteSignInProcedure:          {Rule: Public},
	authnv1connect.MultifactorServiceBeginTotpEnrollmentProcedure:     {Rule: Session},
	authnv1connect.MultifactorServiceConfirmTotpEnrollmentProcedure:   {Rule: Session},
	authnv1connect.MultifactorServiceListTotpEnrollmentsProcedure:     {Rule: Session},
	authnv1connect.MultifactorServiceDeleteTotpEnrollmentProcedure:    {Rule: Session},
	authnv1connect.MultifactorServiceRegenerateRecoveryCodesProcedure: {Rule: Session},
	authnv1connect.MultifactorServiceDisableMfaProcedure:              {Rule: Session},
	// The step-up verification runs on the caller's own token — the code it
	// spends proves the holder, the session only says whose set to look in.
	// The administrative disable is the operator's door over a named
	// account: the administrative session is the authority the procedure
	// carries, because the reason it runs is that the holder has nothing
	// left to prove.
	authnv1connect.MultifactorServiceVerifyRecoveryCodeProcedure: {Rule: Session},
	authnv1connect.MultifactorServiceAdminDisableMfaProcedure:    {Rule: Admin},

	// The password recovery surfaces split at the same line. The trigger and
	// the spend are reached before any token exists — a caller who lost the
	// password holds no credential, and the reset token is the credential
	// ResetPassword judges — so both are Public. The admin trigger hands the
	// reset to a named account, which is administrative work; the guard's
	// refusal of an impersonated caller keeps an administrator from moving
	// the account they are wearing. The cooldown is the feature's, not the
	// contract's: the table cannot express a window.
	authnv1connect.PasswordRecoveryServiceForgotPasswordProcedure:         {Rule: Public},
	authnv1connect.PasswordRecoveryServiceResetPasswordProcedure:          {Rule: Public},
	authnv1connect.PasswordRecoveryServiceAdminResetUserPasswordProcedure: {Rule: Admin},

	// The refresh is the sign-in a caller makes with the pair's other half:
	// the credential the procedure spends is the body's refresh token, and
	// the access token a renewal is fixing may already be expired, so the
	// bearer header is no requirement of it. The service judges the token
	// itself — an unknown, spent, or rotated one answers the ended-session
	// refusal — and a presented bearer is read only to give the procedure
	// the client facts a record rides with.
	authnv1connect.SessionServiceRefreshProcedure: {Rule: Public},

	// The audit trail. `List` is the caller's own history, so being
	// authenticated is the whole requirement — except that a delegated
	// session is refused, which `Self` would express by comparing the
	// account's identifier against a field the request does not have.
	// `Authenticated` is the rule here for the same reason `SendEmail` uses
	// it: the target is the claims' subject, not something the request names.
	//
	// The other three are administrative: an account may read its own
	// activity, never another's, and the facets are derived from every
	// record in the table.
	auditlogv1connect.AuditLogServiceListProcedure:          {Rule: Authenticated},
	auditlogv1connect.AuditLogServiceListAllProcedure:       {Rule: Admin},
	auditlogv1connect.AuditLogServiceListForUserProcedure:   {Rule: Admin},
	auditlogv1connect.AuditLogServiceFilterOptionsProcedure: {Rule: Admin},

	// The caller's own door: the procedure reads the account from the claims
	// and takes no target from the request, so being authenticated is the
	// whole requirement. Upstream answers this with a separate `/users/me`
	// route; here the account is the claims' subject, which is the same rule
	// without a second route.
	//
	// An impersonated caller is refused here by the rule itself, which is
	// what makes `Authenticated` fit the audit trail's self listing too: an
	// account's own activity is not a surface a delegation may read.
	identityv1connect.EmailVerificationServiceSendEmailProcedure: {Rule: Authenticated},

	// Self-service with a target in the request: the account named must be
	// the caller's own.
	identityv1connect.UserServiceResetProfilePictureProcedure: {
		Rule:      Self("id"),
		Field:     "id",
		Prototype: &identityv1.ResetProfilePictureRequest{},
	},

	// The account's own door, beside the administrative surface: the target
	// is the token's subject and the request names nothing, so being
	// authenticated is the whole requirement — the same reason the audit
	// trail's self listing and the verification mail carry `Authenticated`.
	// A delegated caller is refused by the rule itself: a session opened for
	// another account is not the door to that account's own profile.
	identityv1connect.UserServiceGetCurrentUserProcedure:    {Rule: Authenticated},
	identityv1connect.UserServiceUpdateCurrentUserProcedure: {Rule: Authenticated},

	// Administrative, declared explicitly rather than left to the default so
	// the table reads as the complete policy of the surface. Upstream guards
	// every one of these with its admin-required middleware.
	identityv1connect.SignupServiceCreateSignupTokenProcedure:      {Rule: Admin},
	identityv1connect.SignupServiceListSignupTokensProcedure:       {Rule: Admin},
	identityv1connect.SignupServiceDeleteSignupTokenProcedure:      {Rule: Admin},
	identityv1connect.UserServiceListUsersProcedure:                {Rule: Admin},
	identityv1connect.UserServiceGetUserProcedure:                  {Rule: Admin},
	identityv1connect.UserServiceCreateUserProcedure:               {Rule: Admin},
	identityv1connect.UserServiceUpdateUserProcedure:               {Rule: Admin},
	identityv1connect.UserServiceDeleteUserProcedure:               {Rule: Admin},
	identityv1connect.UserServiceBanUserProcedure:                  {Rule: Admin},
	identityv1connect.UserServiceUnbanUserProcedure:                {Rule: Admin},
	identityv1connect.UserGroupServiceListUserGroupsProcedure:      {Rule: Admin},
	identityv1connect.UserGroupServiceGetUserGroupProcedure:        {Rule: Admin},
	identityv1connect.UserGroupServiceCreateUserGroupProcedure:     {Rule: Admin},
	identityv1connect.UserGroupServiceUpdateUserGroupProcedure:     {Rule: Admin},
	identityv1connect.UserGroupServiceDeleteUserGroupProcedure:     {Rule: Admin},
	identityv1connect.UserGroupServiceSetUserGroupMembersProcedure: {Rule: Admin},
	identityv1connect.UserGroupServiceGetUserGroupsProcedure:       {Rule: Admin},
	identityv1connect.UserGroupServiceUpdateUserGroupsProcedure:    {Rule: Admin},
	// The group-side client allowlist is the group's own write — the
	// mirror of the member set the surface owns — so it is administrative
	// like the group's other procedures are.
	identityv1connect.UserGroupServiceSetAllowedOidcClientsProcedure: {Rule: Admin},

	// The authorization surface is administrative end to end: shaping who
	// may act is the one power the administrator role keeps to itself,
	// because a surface that granted its own management could raise
	// anything to itself. Declared explicitly rather than left to the
	// default so the table reads as the complete policy of the surface.
	authzv1connect.AuthorizationServiceListPermissionsProcedure:     {Rule: Admin},
	authzv1connect.AuthorizationServiceListRolesProcedure:           {Rule: Admin},
	authzv1connect.AuthorizationServiceGetRoleProcedure:             {Rule: Admin},
	authzv1connect.AuthorizationServiceCreateRoleProcedure:          {Rule: Admin},
	authzv1connect.AuthorizationServiceUpdateRoleProcedure:          {Rule: Admin},
	authzv1connect.AuthorizationServiceDeleteRoleProcedure:          {Rule: Admin},
	authzv1connect.AuthorizationServiceSetRolePermissionsProcedure:  {Rule: Admin},
	authzv1connect.AuthorizationServiceListUserRolesProcedure:       {Rule: Admin},
	authzv1connect.AuthorizationServiceSetUserRolesProcedure:        {Rule: Admin},
	authzv1connect.AuthorizationServiceListUserPermissionsProcedure: {Rule: Admin},
	authzv1connect.AuthorizationServiceSetUserPermissionsProcedure:  {Rule: Admin},

	// The API keys' own surface is session-only: a key cannot manage keys,
	// the refusal the upstream spells with a middleware switch and this
	// surface spells with a rule against the caller's credential kind. The
	// administrative view over every key is a plain admin procedure.
	apikeyv1connect.ApiKeyServiceCreateAPIKeyProcedure:   {Rule: Session},
	apikeyv1connect.ApiKeyServiceListAPIKeysProcedure:    {Rule: Session},
	apikeyv1connect.ApiKeyServiceRenewAPIKeyProcedure:    {Rule: Session},
	apikeyv1connect.ApiKeyServiceRevokeAPIKeyProcedure:   {Rule: Session},
	apikeyv1connect.ApiKeyServiceListAllAPIKeysProcedure: {Rule: Admin},

	// The session lifecycle is the caller's own: every procedure acts on the
	// session the claims name or the account it belongs to, so the session
	// rule is the whole requirement — a machine credential has no session
	// behind it and is refused with the keys' surface, and a caller without
	// the sid claim is not a session at all. Refresh is the exception, read
	// with the public surfaces above: its credential is the body's token.
	authnv1connect.SessionServiceSignOutProcedure:              {Rule: Session},
	authnv1connect.SessionServiceGetSessionProcedure:           {Rule: Session},
	authnv1connect.SessionServiceListSessionsProcedure:         {Rule: Session},
	authnv1connect.SessionServiceRevokeSessionProcedure:        {Rule: Session},
	authnv1connect.SessionServiceSignOutOtherSessionsProcedure: {Rule: Session},
	authnv1connect.SessionServiceSignOutAllSessionsProcedure:   {Rule: Session},

	// The delegation pair. ImpersonateUser is administrative work over a
	// session, so the admin rule is the whole requirement — and the service
	// beneath it adds what a rule cannot express: the target must exist, must
	// not be an administrator, and must not be the caller. StopImpersonating
	// is the one procedure a delegated caller must reach: its rule requires
	// the delegation instead of refusing it, because the way out of a
	// borrowed identity must not be closed by the refusals the other rules
	// keep — and it still refuses a machine credential, which has no session
	// to end.
	authnv1connect.SessionServiceImpersonateUserProcedure:   {Rule: Admin},
	authnv1connect.SessionServiceStopImpersonatingProcedure: {Rule: StopImpersonating},

	// The notifications split one service at the same line the keys do: the
	// four administrative procedures publish and withdraw what every account
	// reads, and the account procedures read the caller's own inbox — the
	// account is the claims' subject, so there is no field for `Self` to
	// compare. The watch stream is the same read held open.
	notificationv1connect.NotificationServiceCreateNotificationProcedure:       {Rule: Admin},
	notificationv1connect.NotificationServiceGetNotificationProcedure:          {Rule: Admin},
	notificationv1connect.NotificationServiceListAllNotificationsProcedure:     {Rule: Admin},
	notificationv1connect.NotificationServiceCancelNotificationProcedure:       {Rule: Admin},
	notificationv1connect.NotificationServiceListNotificationsProcedure:        {Rule: Authenticated},
	notificationv1connect.NotificationServiceMarkNotificationReadProcedure:     {Rule: Authenticated},
	notificationv1connect.NotificationServiceMarkAllNotificationsReadProcedure: {Rule: Authenticated},
	notificationv1connect.NotificationServiceUnreadCountProcedure:              {Rule: Authenticated},
	notificationv1connect.NotificationServiceWatchNotificationsProcedure:       {Rule: Authenticated},

	// The application-configuration surface is the deployment's own settings.
	// The test-email send is administrative: it proves the mailer from the
	// settings screen, so it is not an account read, and there is no self
	// rule to name a field for. The configuration read is REST —
	// `GET /api/configuration` in RestRules — not a procedure here.
	systemv1connect.AppConfigServiceTestEmailProcedure: {Rule: Admin},

	// The settings surface is the database-backed, product-flow settings.
	// ListPublic is the one public read: it publishes only the catalog items
	// flagged public, and a public item never rests sealed — the catalog
	// refuses that pair — so an unauthenticated caller can never reach a
	// ciphertext. The rest is administrative: the values travel in the
	// clear, which is not an account read either.
	settingsv1connect.SettingsServiceListProcedure:       {Rule: Admin},
	settingsv1connect.SettingsServiceUpdateProcedure:     {Rule: Admin},
	settingsv1connect.SettingsServiceResetProcedure:      {Rule: Admin},
	settingsv1connect.SettingsServiceListPublicProcedure: {Rule: Public},

	// The OIDC client surface is administrative end to end: deciding which
	// applications a deployment signs accounts into is the operator's
	// power, the way the authorization surface is. Declared explicitly
	// rather than left to the default so the table reads as the complete
	// policy of the surface. The protocol endpoints the clients themselves
	// speak are REST routes, declared beside the routes below when they
	// land.
	federationv1connect.OidcClientServiceListClientsProcedure:             {Rule: Admin},
	federationv1connect.OidcClientServiceCreateClientProcedure:            {Rule: Admin},
	federationv1connect.OidcClientServiceGetClientProcedure:               {Rule: Admin},
	federationv1connect.OidcClientServiceUpdateClientProcedure:            {Rule: Admin},
	federationv1connect.OidcClientServiceDeleteClientProcedure:            {Rule: Admin},
	federationv1connect.OidcClientServiceUpdateAllowedUserGroupsProcedure: {Rule: Admin},
	federationv1connect.OidcClientServiceGetClientMetaProcedure:           {Rule: Admin},
	federationv1connect.OidcClientServicePreviewClientProcedure:           {Rule: Admin},
	federationv1connect.OidcClientServiceUploadLogoProcedure:              {Rule: Admin},
	federationv1connect.OidcClientServiceDeleteLogoProcedure:              {Rule: Admin},
	federationv1connect.OidcClientServiceListSecretsProcedure:             {Rule: Admin},
	federationv1connect.OidcClientServiceCreateSecretProcedure:            {Rule: Admin},
	federationv1connect.OidcClientServiceDeleteSecretProcedure:            {Rule: Admin},
	federationv1connect.OidcClientServiceRefreshClientProcedure:           {Rule: Admin},

	// The custom-claim surface is administrative end to end, like the
	// client surface beside it: a claim rides every token the account or
	// its groups produce, so defining one is deciding what a foreign
	// client learns.
	federationv1connect.CustomClaimServiceSuggestProcedure:          {Rule: Admin},
	federationv1connect.CustomClaimServiceListUserClaimsProcedure:   {Rule: Admin},
	federationv1connect.CustomClaimServiceCreateUserClaimProcedure:  {Rule: Admin},
	federationv1connect.CustomClaimServiceUpdateUserClaimProcedure:  {Rule: Admin},
	federationv1connect.CustomClaimServiceDeleteUserClaimProcedure:  {Rule: Admin},
	federationv1connect.CustomClaimServiceListGroupClaimsProcedure:  {Rule: Admin},
	federationv1connect.CustomClaimServiceCreateGroupClaimProcedure: {Rule: Admin},
	federationv1connect.CustomClaimServiceUpdateGroupClaimProcedure: {Rule: Admin},
	federationv1connect.CustomClaimServiceDeleteGroupClaimProcedure: {Rule: Admin},

	// The consent surface splits in two: the self-service procedures read
	// and undo the calling account's own ledger — being signed in is the
	// whole requirement, the account rides the token — while the two
	// administrative reads name the account in the request and demand the
	// role. Declared explicitly, like the surfaces above.
	federationv1connect.OidcConsentServiceListMyAuthorizedClientsProcedure:   {Rule: Authenticated},
	federationv1connect.OidcConsentServiceRevokeMyAuthorizedClientProcedure:  {Rule: Authenticated},
	federationv1connect.OidcConsentServiceListMyClientsProcedure:             {Rule: Authenticated},
	federationv1connect.OidcConsentServiceListUserAuthorizedClientsProcedure: {Rule: Admin},
	federationv1connect.OidcConsentServiceListAllAuthorizedClientsProcedure:  {Rule: Admin},
}

// RuleFor answers the rule a procedure gets. A procedure the table does not
// name is administrative, so a new procedure is protected the moment it is
// mounted.
func RuleFor(procedure string) Rule {
	if entry, ok := ProcedureRules[procedure]; ok && entry.Rule != nil {
		return entry.Rule
	}
	return Admin
}

// RestEntry declares the rule one REST route gets.
type RestEntry struct {
	// Method is the HTTP method the route answers.
	Method string
	// Pattern is the chi route pattern, with `{param}` for one segment.
	Pattern string
	// Rule decides who may call the route.
	Rule Rule
	// Param is the path parameter a self rule compares against the caller.
	Param string
}

// RestRules is the rule every REST route a module mounts gets.
//
// Like the procedure table, a route absent from it is administrative. The
// routes the application mounts itself — the API root, the readiness probe —
// are outside the guarded group and never reach this table.
var RestRules = []RestEntry{
	// The protocol surfaces a client reaches without a token. The key set is
	// public because a verifier needs it before it can hold a token; the
	// picture read is public because an <img> tag fetches it, and an account
	// without one answers the bundled default by redirect.
	{Method: http.MethodGet, Pattern: "/.well-known/jwks.json", Rule: Public},
	{Method: http.MethodGet, Pattern: "/.well-known/openid-configuration", Rule: Public},
	{Method: http.MethodGet, Pattern: "/api/users/{id}/profile-picture.png", Rule: Public},

	// The `/me` writes come before the `/{id}` write: the table is matched
	// in order, and a `{id}` pattern checked first would swallow `me` and
	// compare the word against a subject — the refusal a real identifier
	// earns, applied to a route that names no account at all.
	//
	// They are the account's own by construction: no identifier travels, so
	// the target is the caller the bearer middleware verified, and being
	// authenticated is the whole requirement. The rules are the `/{id}`
	// write's answer to the routes upstream offers beside it.
	{Method: http.MethodPut, Pattern: "/api/users/me/profile-picture", Rule: Authenticated},
	{Method: http.MethodDelete, Pattern: "/api/users/me/profile-picture", Rule: Authenticated},

	// The write is the account's own: the caller must be the account named in
	// the path. An administrator does not pass by virtue of the role — the
	// administrative procedures are the RPC surface's — which is the rule
	// upstream applies by offering `/users/me/profile-picture` separately.
	{
		Method:  http.MethodPut,
		Pattern: "/api/users/{id}/profile-picture",
		Rule:    Self("id"),
		Param:   "id",
	},

	// The upload progress is a signed-in read, not an owner-scoped one: the
	// key a progress poll names is the storage key a client already holds,
	// and the answer — a status word and a byte size — is the same fact the
	// file's own read carries. The catch-all is the rule's shape because a
	// storage key carries slashes inside it.
	{Method: http.MethodGet, Pattern: "/api/uploads/{key...}", Rule: Authenticated},

	// The configuration read is the deployment's bootstrap document: the
	// public subset to an anonymous caller, the full non-secret document to
	// an administrator's token. The middleware authenticates
	// opportunistically on a public route — a token is verified when
	// presented, an anonymous caller is never refused — and the handler
	// reads the caller the context carries.
	{Method: http.MethodGet, Pattern: "/api/configuration", Rule: Public},

	// The client logo is the OIDC sign-in page's asset: an <img> tag
	// fetches it before any credential exists, the way the account
	// pictures are read. A client without a logo answers not found, never
	// a substitute.
	{Method: http.MethodGet, Pattern: "/oidc/clients/{id}/logo", Rule: Public},
	// The OIDC protocol surface: the wire shapes the specifications
	// define, every one public in the bearer sense — the token endpoint
	// authenticates its client from the form or the Basic header, the
	// interaction callback from the SPA's own credential, and the
	// authorize endpoint opens the browser flow.
	{Method: http.MethodGet, Pattern: "/oidc/authorize", Rule: Public},
	{Method: http.MethodPost, Pattern: "/oidc/authorize", Rule: Public},
	{Method: http.MethodGet, Pattern: "/oidc/authorize/*", Rule: Public},
	{Method: http.MethodPost, Pattern: "/oidc/authorize/*", Rule: Public},
	{Method: http.MethodPost, Pattern: "/oidc/token", Rule: Public},
	{Method: http.MethodGet, Pattern: "/oidc/userinfo", Rule: Public},
	{Method: http.MethodPost, Pattern: "/oidc/userinfo", Rule: Public},
	{Method: http.MethodGet, Pattern: "/oidc/end-session", Rule: Public},
	{Method: http.MethodPost, Pattern: "/oidc/end-session", Rule: Public},
	// Introspection and PAR are client-authenticated from the form or the
	// Basic header, the way the token endpoint is: public in the bearer
	// sense, the library refusing a request whose secret does not verify.
	{Method: http.MethodPost, Pattern: "/oidc/introspect", Rule: Public},
	{Method: http.MethodPost, Pattern: "/oidc/par", Rule: Public},
}

// ContractProcedures lists every procedure path the contracts declare.
//
// It is derived from the generated descriptors rather than written out, so
// the table above can be checked against the contract itself: a rule attached
// to a procedure that no longer exists fails a test instead of becoming a
// guard nothing ever reaches.
func ContractProcedures() []string {
	files := []protoreflect.FileDescriptor{
		auditlogv1.File_auditlog_proto,
		authnv1.File_authn_proto,
		authnv1.File_one_time_access_proto,
		apikeyv1.File_api_key_proto,
		authzv1.File_authz_proto,
		federationv1.File_federation_proto,
		identityv1.File_identity_proto,
		notificationv1.File_notification_proto,
		settingsv1.File_settings_proto,
		systemv1.File_system_proto,
	}

	var procedures []string
	for _, file := range files {
		services := file.Services()
		for i := range services.Len() {
			service := services.Get(i)
			methods := service.Methods()
			for j := range methods.Len() {
				procedures = append(procedures,
					"/"+string(service.FullName())+"/"+string(methods.Get(j).Name()))
			}
		}
	}
	return procedures
}

// PublicProcedures is the set the bearer middleware answers without a caller.
// It is derived from the table above rather than kept beside it, so a
// procedure cannot be public for the authenticator and administrative for the
// guard.
func PublicProcedures() map[string]struct{} {
	public := make(map[string]struct{})
	for procedure, entry := range ProcedureRules {
		if IsPublic(entry.Rule) {
			public[procedure] = struct{}{}
		}
	}
	return public
}

// MatchRest answers the rule a REST request gets, with the target it names.
//
// The route is matched segment by segment, so a `{param}` stands for exactly
// one segment and a path parameter is read from the position the pattern puts
// it in. Matching here rather than reading chi's route context is deliberate:
// the middleware runs before the route is matched, and a guard that waited for
// the match would run after the handler was chosen.
//
// A route the tables do not name is administrative, the same default the
// procedure table applies.
func MatchRest(rules []RestEntry, method, path string) (Rule, Target) {
	segments := splitPath(path)
	for _, entry := range rules {
		if entry.Method != method {
			continue
		}
		params, ok := matchPattern(splitPath(entry.Pattern), segments)
		if !ok {
			continue
		}
		rule := entry.Rule
		if rule == nil {
			rule = Admin
		}
		return rule, Target{PathParams: params}
	}
	return Admin, Target{}
}

// CallerOf reads the caller an authenticator resolved.
//
// The authenticator answers the value the context carries, and this is the one
// place that value is turned back into a caller, so a middleware never has to
// know the concrete type. A value that is not a caller — a test's stub — is
// nobody, which every rule that needs a caller refuses.
func CallerOf(info any) *jwtutils.Caller {
	caller, _ := info.(*jwtutils.Caller)
	return caller
}

// matchPattern compares a route pattern against a path, answering the path
// parameters the pattern captured. A pattern with a different segment count
// cannot match, so `{id}` never stands for the rest of a path.
func matchPattern(pattern, path []string) (map[string]string, bool) {
	if len(pattern) != len(path) {
		// A trailing catch-all is the one shape a shorter path cannot
		// preclude: the check below decides it by prefix.
		if len(pattern) == 0 || !isWildcard(pattern[len(pattern)-1]) || len(path) < len(pattern) {
			return nil, false
		}
	} else if isWildcard(pattern[len(pattern)-1]) && len(path) == len(pattern) {
		// A catch-all with nothing left to absorb names nothing.
		return nil, false
	}

	var params map[string]string
	for i, segment := range pattern {
		if isWildcard(segment) {
			// The storage keys carry slashes, so a route that names one
			// absorbs the rest of the path; the wildcard is the pattern's
			// last segment and answers at least one segment of it.
			if i != len(pattern)-1 {
				return nil, false
			}
			if params == nil {
				params = make(map[string]string, 1)
			}
			name, _ := wildcardName(segment)
			params[name] = strings.Join(path[i:], "/")
			return params, true
		}
		if name, ok := paramName(segment); ok {
			if params == nil {
				params = make(map[string]string, 1)
			}
			params[name] = path[i]
			continue
		}
		if segment != path[i] {
			return nil, false
		}
	}
	return params, true
}

// isWildcard reports whether a pattern segment is the trailing catch-all
// `{name...}`: the one segment that absorbs the rest of the path, for the
// keys a route names that carry slashes inside them.
func isWildcard(segment string) bool {
	_, ok := wildcardName(segment)
	return ok
}

// wildcardName reads the `{name...}` segment's name.
func wildcardName(segment string) (string, bool) {
	if len(segment) < 6 || !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "...}") {
		return "", false
	}
	name := segment[1 : len(segment)-4]
	if name == "" || strings.ContainsAny(name, "{}. ") {
		return "", false
	}
	return name, true
}

// paramName reads a `{name}` segment.
func paramName(segment string) (string, bool) {
	if len(segment) < 3 || !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
		return "", false
	}
	return segment[1 : len(segment)-1], true
}

// splitPath splits a URL path into its non-empty segments.
func splitPath(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == '/' })
}
