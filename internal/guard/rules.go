package guard

import (
	"net/http"
	"strings"

	apikeyv1 "github.com/riipandi/tango/codegen/proto/go/tango/apikey/v1"
	apikeyv1connect "github.com/riipandi/tango/codegen/proto/go/tango/apikey/v1/apikeyv1connect"
	auditlogv1 "github.com/riipandi/tango/codegen/proto/go/tango/auditlog/v1"
	auditlogv1connect "github.com/riipandi/tango/codegen/proto/go/tango/auditlog/v1/auditlogv1connect"
	authv1 "github.com/riipandi/tango/codegen/proto/go/tango/auth/v1"
	authv1connect "github.com/riipandi/tango/codegen/proto/go/tango/auth/v1/authv1connect"
	identityv1 "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1"
	identityv1connect "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1/identityv1connect"
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
	authv1connect.AuthServiceSignInProcedure:                       {Rule: Public},
	identityv1connect.SignupServiceSignupProcedure:                 {Rule: Public},
	identityv1connect.EmailVerificationServiceVerifyEmailProcedure: {Rule: Public},

	// The one-time access codes. The exchange is the sign-in a caller makes
	// with a code instead of a password, so it is reached before any token
	// exists; the public email ask is reached from the sign-in page for the
	// same reason. The two administrative procedures hand a credential to an
	// account's owner or drive the mailer at one address, which is
	// administrative work on an account.
	authv1connect.OneTimeAccessServiceExchangeTokenProcedure:       {Rule: Public},
	authv1connect.OneTimeAccessServiceRequestEmailProcedure:        {Rule: Public},
	authv1connect.OneTimeAccessServiceCreateTokenProcedure:         {Rule: Admin},
	authv1connect.OneTimeAccessServiceRequestEmailAsAdminProcedure: {Rule: Admin},

	// The multifactor surfaces split at the same line the one-time access
	// codes do. CompleteSignIn is reached with the pending token the
	// password check minted — the caller holds no access token yet, and the
	// pending token is the credential the procedure judges. The enrollment,
	// listing, regeneration, and switch-off run on the caller's own token,
	// so `Session` names them: a machine credential has no second factor to
	// manage, and the refusal of an impersonated caller keeps an
	// administrator from enrolling a factor onto the account they are
	// wearing — the delegation must end before the account's guard changes.
	authv1connect.MultifactorServiceCompleteSignInProcedure:          {Rule: Public},
	authv1connect.MultifactorServiceBeginTotpEnrollmentProcedure:     {Rule: Session},
	authv1connect.MultifactorServiceConfirmTotpEnrollmentProcedure:   {Rule: Session},
	authv1connect.MultifactorServiceListTotpEnrollmentsProcedure:     {Rule: Session},
	authv1connect.MultifactorServiceDeleteTotpEnrollmentProcedure:    {Rule: Session},
	authv1connect.MultifactorServiceRegenerateRecoveryCodesProcedure: {Rule: Session},
	authv1connect.MultifactorServiceDisableMfaProcedure:              {Rule: Session},

	// The password recovery surfaces split at the same line. The trigger and
	// the spend are reached before any token exists — a caller who lost the
	// password holds no credential, and the reset token is the credential
	// ResetPassword judges — so both are Public. The admin trigger hands the
	// reset to a named account, which is administrative work; the guard's
	// refusal of an impersonated caller keeps an administrator from moving
	// the account they are wearing. The cooldown is the feature's, not the
	// contract's: the table cannot express a window.
	authv1connect.PasswordRecoveryServiceForgotPasswordProcedure:         {Rule: Public},
	authv1connect.PasswordRecoveryServiceResetPasswordProcedure:          {Rule: Public},
	authv1connect.PasswordRecoveryServiceAdminResetUserPasswordProcedure: {Rule: Admin},

	// The refresh is the sign-in a caller makes with the pair's other half:
	// the credential the procedure spends is the body's refresh token, and
	// the access token a renewal is fixing may already be expired, so the
	// bearer header is no requirement of it. The service judges the token
	// itself — an unknown, spent, or rotated one answers the ended-session
	// refusal — and a presented bearer is read only to give the procedure
	// the client facts a record rides with.
	authv1connect.SessionServiceRefreshProcedure: {Rule: Public},

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
	authv1connect.SessionServiceSignOutProcedure:              {Rule: Session},
	authv1connect.SessionServiceGetSessionProcedure:           {Rule: Session},
	authv1connect.SessionServiceListSessionsProcedure:         {Rule: Session},
	authv1connect.SessionServiceRevokeSessionProcedure:        {Rule: Session},
	authv1connect.SessionServiceSignOutOtherSessionsProcedure: {Rule: Session},
	authv1connect.SessionServiceSignOutAllSessionsProcedure:   {Rule: Session},

	// The delegation pair. ImpersonateUser is administrative work over a
	// session, so the admin rule is the whole requirement — and the service
	// beneath it adds what a rule cannot express: the target must exist, must
	// not be an administrator, and must not be the caller. StopImpersonating
	// is the one procedure a delegated caller must reach: its rule requires
	// the delegation instead of refusing it, because the way out of a
	// borrowed identity must not be closed by the refusals the other rules
	// keep — and it still refuses a machine credential, which has no session
	// to end.
	authv1connect.SessionServiceImpersonateUserProcedure:   {Rule: Admin},
	authv1connect.SessionServiceStopImpersonatingProcedure: {Rule: StopImpersonating},
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
}

// RestRuleFor answers the rule a REST request gets, with the target it names.
//
// The route is matched segment by segment, so a `{param}` stands for exactly
// one segment and a path parameter is read from the position the pattern puts
// it in. Matching here rather than reading chi's route context is deliberate:
// the middleware runs before the route is matched, and a guard that waited
// for the match would run after the handler was chosen.
//
// A route the table does not name is administrative, the same default the
// procedure table applies.
func RestRuleFor(method, path string) (Rule, Target) {
	segments := splitPath(path)
	for _, entry := range RestRules {
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

// ContractProcedures lists every procedure path the contracts declare.
//
// It is derived from the generated descriptors rather than written out, so
// the table above can be checked against the contract itself: a rule attached
// to a procedure that no longer exists fails a test instead of becoming a
// guard nothing ever reaches.
func ContractProcedures() []string {
	files := []protoreflect.FileDescriptor{
		auditlogv1.File_auditlog_proto,
		authv1.File_auth_proto,
		authv1.File_one_time_access_proto,
		apikeyv1.File_api_key_proto,
		identityv1.File_identity_proto,
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
		return nil, false
	}
	var params map[string]string
	for i, segment := range pattern {
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
