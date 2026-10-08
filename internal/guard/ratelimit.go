package guard

import (
	"strings"

	authnv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1/authnv1connect"
	identityv1connect "github.com/riipandi/saka/codegen/proto/go/saka/identity/v1/identityv1connect"
	settingsv1connect "github.com/riipandi/saka/codegen/proto/go/saka/settings/v1/settingsv1connect"
	systemv1connect "github.com/riipandi/saka/codegen/proto/go/saka/system/v1/systemv1connect"
)

// The rate-limit policy tables: which procedures the limiter counts, and in
// which bucket. The guard owns them because they are request policy, the same
// way the rules tables are — a procedure's throttle is decided beside its
// authorization, and the same derived-check pattern keeps the tables honest
// against the contracts.
//
// A procedure named in neither table is NOT counted: reads, administrative
// writes, and every call a bearer-guarded caller makes are outside the
// limiter's books. The counted surface is the one an unauthenticated caller
// reaches — the credential attempts and the email senders — plus the refresh,
// which is public and mints tokens.

// RateBuckets names the throttle class a procedure is counted under.
const (
	// RateAuth is the credential bucket: the sign-in, the sign-up, the code
	// and token verifications, and the email senders. Its budget is the one
	// an attacker's script meets first.
	RateAuth = "auth"
	// RateDefault is the budget every other counted procedure shares.
	RateDefault = "default"
)

// RateAuthProcedures names the procedures counted under the credential
// bucket. They are the surface a script drives: guessing a password, a
// six-character code, or a TOTP, and provoking the mailer into a bomb. The
// budget they share is rate_limit.auth_limit; the feature-level cooldowns and
// single-use spend limits stand beside it, not instead of it.
var RateAuthProcedures = map[string]struct{}{
	authnv1connect.AuthServiceSignInProcedure:                             {},
	identityv1connect.SignupServiceSignupProcedure:                        {},
	authnv1connect.OneTimeAccessServiceExchangeTokenProcedure:             {},
	identityv1connect.EmailVerificationServiceVerifyEmailProcedure:        {},
	identityv1connect.EmailVerificationServiceConfirmEmailChangeProcedure: {},
	authnv1connect.PasswordRecoveryServiceResetPasswordProcedure:          {},
	authnv1connect.PasswordRecoveryServiceForgotPasswordProcedure:         {},
	authnv1connect.OneTimeAccessServiceRequestEmailProcedure:              {},
	authnv1connect.MultifactorServiceCompleteSignInProcedure:              {},
	// The enrollment pair is public only because the `mfa.required` bridge
	// admits a caller no token names: the bridge is the credential the
	// procedures judge, so a drive against it shares the credential
	// budget's stakes.
	authnv1connect.MultifactorServiceBeginTotpEnrollmentProcedure:   {},
	authnv1connect.MultifactorServiceConfirmTotpEnrollmentProcedure: {},
	// The reauthentication door carries the same budget as the sign-in: its
	// password half is a credential guess like any other, and the locked-in
	// session changes the stakes, not the shape — the proof's budget is what
	// makes a drive against the account's password expensive.
	authnv1connect.WebAuthnServiceReauthenticateProcedure: {},
	// The code send provokes one email per call — the same mailbomb a
	// driving sign-in code request is — so it shares the credential bucket.
	authnv1connect.WebAuthnServiceSendReauthenticationCodeProcedure: {},
	// The passkey assertion is a credential attempt like the password: the
	// verification is the guess, and the budget the bucket shares is what
	// makes a drive against the credential space expensive.
	authnv1connect.WebAuthnServiceVerifyLoginProcedure: {},
	// The OAuth continue is a credential spend like the second factor's
	// complete: the flow token is the single-use credential the procedure
	// judges, and its success mints the token pair. The email-code spend
	// rides the same bucket — the code is the guess, three wrong answers
	// end the flow.
	authnv1connect.OAuthSSOServiceContinueSignInProcedure:    {},
	authnv1connect.OAuthSSOServiceVerifySignInEmailProcedure: {},
	// The email senders an authenticated or administrative caller reaches:
	// a compromised account or an impatient operator must not become a
	// mailbomb, so they count against the same tight budget as the public
	// attempts.
	identityv1connect.EmailVerificationServiceSendEmailProcedure:          {},
	identityv1connect.EmailVerificationServiceRequestEmailChangeProcedure: {},
	authnv1connect.OneTimeAccessServiceRequestEmailAsAdminProcedure:       {},
	authnv1connect.PasswordRecoveryServiceAdminResetUserPasswordProcedure: {},
}

// RateDefaultProcedures names the procedures counted under the default
// bucket — public work that is not a credential attempt: the refresh mints
// tokens and answers before any credential of its own.
var RateDefaultProcedures = map[string]struct{}{
	authnv1connect.SessionServiceRefreshProcedure: {},
	// The ceremony openers write a challenge row apiece: public work that
	// is not a credential attempt, but whose spam fills a table, so they
	// count against the default budget.
	authnv1connect.WebAuthnServiceBeginLoginProcedure:         {},
	authnv1connect.WebAuthnServiceBeginRegistrationProcedure:  {},
	authnv1connect.WebAuthnServiceVerifyRegistrationProcedure: {},
	// The OAuth begin writes a flow row apiece, the same table-filling
	// shape the ceremony openers above carry.
	authnv1connect.OAuthSSOServiceBeginSignInProcedure: {},
}

// RateExemptProcedures names the public procedures the limiter never counts —
// the public reads, whose answers are cheap and whose abuse an IP already
// pays for in bandwidth, and the readiness probe the exclusion list also
// carries.
var RateExemptProcedures = map[string]struct{}{
	systemv1connect.HealthServiceCheckProcedure:                   {},
	settingsv1connect.SettingsServiceListPublicProcedure:          {},
	authnv1connect.OAuthSSOServiceListEnabledConnectionsProcedure: {},
}

// rpcPathPrefix is the URL prefix the RPC surface is mounted under. The
// procedure tables name procedures without it, so a URL path loses it before
// the lookup.
const rpcPathPrefix = "/rpc"

// RateAuthRestPaths names the REST paths the limiter counts under the
// credential bucket — the protocol endpoints a token is bought at, where
// the attempt is the abuse. The revocation endpoint carries the same
// shape: a credential-bearing call a flood of invalid secrets is abuse
// at. The PAR endpoint authenticates the same
// clients and issues one-time request URIs, so it rides the bucket too.
// The authorize and userinfo surfaces are exempt: the redirect is free,
// and the bearer already paid.
var RateAuthRestPaths = map[string]struct{}{
	"/oidc/token":                {},
	"/oidc/revoke":               {},
	"/oidc/par":                  {},
	"/oidc/device_authorization": {},
}

// RateBucketFor answers the bucket a procedure is counted under, or false
// when the limiter never counts it. The path is the URL path the RPC surface
// serves, with or without its /rpc prefix; the REST protocol paths the
// tables name are full URL paths.
func RateBucketFor(path string) (string, bool) {
	if _, ok := RateAuthRestPaths[strings.TrimSuffix(path, "/")]; ok {
		return RateAuth, true
	}
	path = strings.TrimPrefix(path, rpcPathPrefix)
	if _, ok := RateAuthProcedures[path]; ok {
		return RateAuth, true
	}
	if _, ok := RateDefaultProcedures[path]; ok {
		return RateDefault, true
	}
	return "", false
}
