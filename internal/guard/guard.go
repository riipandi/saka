// Package guard holds the authorization rules the transport enforces on a
// request, and the tables that declare which rule each procedure and route
// gets.
//
// It exists because a route table is the wrong place to answer "who may call
// this": the answer depends on the caller's claims, and a table that mixed
// routing with claims would be read by two audiences with nothing in common.
// The transport owns the mechanism — a refusal is written in the protocol the
// caller used — and this package owns the policy: which rule, and what a rule
// compares.
//
// The tables are the surface's contract with itself. A procedure or route
// absent from them is administrative, which is the safe default: forgetting
// to declare something refuses it rather than publishing it.
package guard

import (
	"context"
	"errors"

	"github.com/riipandi/saka/pkg/jwtutils"
)

// The refusals a rule raises. They are sentinel errors because the transport
// maps them to wire codes once, so a rule never decides a status code.
var (
	// ErrUnauthenticated is a request with no authenticated caller.
	ErrUnauthenticated = errors.New("authentication required")

	// ErrAdminRequired is a caller who is authenticated but holds no
	// administrator role.
	ErrAdminRequired = errors.New("administrator role required")

	// ErrPermissionRequired is an authenticated caller whose effective
	// grants do not satisfy the procedure's permission rule.
	ErrPermissionRequired = errors.New("permission required")

	// ErrNotSelf is a self-service request whose target is another account.
	//
	// It is deliberately the same answer an unknown account produces — the
	// transport maps it to `not_found` — so a caller cannot use the refusal
	// to learn whether an account exists. The distinction survives in the
	// audit record and the server log, which an operator reads.
	ErrNotSelf = errors.New("the caller may only act on their own account")

	// ErrMachineCredential is a request the guard's session rule refuses
	// because the caller proved itself through a machine credential. The
	// API keys' own management surface is the rule's home: a credential
	// that cannot revoke itself must not be the one managing credentials.
	//
	// The refusal is the not_found shape like every other, so a key holder
	// probing the surface learns nothing about it.
	ErrMachineCredential = errors.New("a machine credential may not use a session-only request")

	// ErrImpersonated is a request that belongs to the account itself,
	// refused because the caller is acting for another account. An
	// administrator's delegation is not a way to act as somebody else on a
	// surface that is theirs.
	ErrImpersonated = errors.New("an impersonated caller may not use a self-service request")
)

// Target is what a rule compares against the caller: the account a request
// names, wherever the protocol carries it.
//
// The two transports carry it differently — a procedure names it in the
// request message, a REST route in a path segment — so the rule reads a
// Target rather than a message, and one rule serves both. The alternative,
// a rule per transport, is the drift this type exists to prevent.
type Target struct {
	// Message is the decoded request message, for a procedure. It is nil on
	// the REST surface.
	Message any
	// PathParams are the route's path segments, for a REST route. It is nil
	// on the RPC surface.
	PathParams map[string]string
}

// ID reads the account identifier the request names.
//
// The path is consulted first, then the message field, both under the same
// name: a REST route calls its segment `id` and a procedure calls its field
// `id`, so one declaration covers both. The name is the proto field name —
// the snake_case key the contract declares — so a table entry reads like the
// request a client sends.
//
// A name the request does not carry reports false. The rule refuses on it
// rather than comparing against an empty string, because a guard that cannot
// find what it compares must not answer "yes".
func (t Target) ID(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	if value, ok := t.PathParams[name]; ok && value != "" {
		return value, true
	}
	return stringField(t.Message, name)
}

// Rule decides whether a caller may run a request.
//
// The rule reads the caller the authenticator resolved and the target the
// request names, because a self-service rule has to compare the account the
// request names against the account the caller is. It answers nil to allow,
// and one of the sentinel errors above to refuse.
//
// A rule must not reach a database: it runs before the handler, on every
// call, and an authorization decision that needs a round trip belongs in the
// service where the transaction already is.
type Rule func(caller *jwtutils.Caller, target Target) error

// Public answers every request, caller or not.
func Public(*jwtutils.Caller, Target) error { return nil }

// Authenticated answers any verified caller, whatever their role, and refuses
// a delegated one.
//
// It is the rule for a request that is the caller's own by construction — one
// that reads the account from the claims and takes no target from the request.
// That is why it is also the rule for the audit trail's self listing: the
// account is the claims' subject, so there is no field for `Self` to compare,
// and the delegation refusal is what keeps an impersonated session from
// reading the account's own history as if it were the account.
func Authenticated(caller *jwtutils.Caller, _ Target) error {
	if caller == nil {
		return ErrUnauthenticated
	}
	if caller.IsImpersonating() {
		return ErrImpersonated
	}
	return nil
}

// Session answers a caller who proved itself through a session — a signed
// access token that carries the session's identifier — and refuses a machine
// credential.
//
// It is `Authenticated` narrowed by the credential kind, and narrowed again
// by the claim: a session credential names the row it acts on, so a caller
// without the `sid` claim is not a session, whatever signed it. A key that
// could list, create, and revoke sessions or keys would not survive its
// owner's intent: the upstream it ports disables API-key authentication on
// its own management routes, and this rule is that refusal expressed against
// the one caller type instead of a second middleware.
func Session(caller *jwtutils.Caller, _ Target) error {
	if caller == nil {
		return ErrUnauthenticated
	}
	if caller.IsMachine() {
		// The refusal is the not-found shape from here on: a key probing
		// the surface learns nothing about what a session holds.
		return ErrMachineCredential
	}
	if caller.SessionID == "" {
		return ErrUnauthenticated
	}
	if caller.IsImpersonating() {
		return ErrImpersonated
	}
	return nil
}

// Admin answers a caller whose claims name the administrator role. It is the
// default rule, so a request that forgets to declare one is administrative
// rather than open.
//
// The role is what the claims carry — `is_admin` died with the boolean it
// reported — so an administrator is an account the administrator role was
// assigned to, and a token signed before the assignment keeps the access
// until it is renewed.
func Admin(caller *jwtutils.Caller, _ Target) error {
	if caller == nil {
		return ErrUnauthenticated
	}
	if !caller.IsAdministrator() {
		return ErrAdminRequired
	}
	return nil
}

// Reauthenticated answers a caller whose session is augmented by a step-up
// proof — a single-use reauthentication token minted moments ago by a
// password check or a passkey assertion, and spent by exactly one guarded
// call.
//
// The rule itself carries only the classification: an anonymous caller is
// refused here, and the proof's consumption is the transport's job, because
// a rule sees the claims and the message but never the database. The
// interceptor consults RequiresReauthentication, reads the proof's header,
// and spends the token atomically before the procedure runs — so the
// requirement is judged once, in one place, and every procedure the table
// marks inherits it.
//
// A machine credential is refused with the session shape: a key cannot
// produce a proof only a present human can, and the surfaces this rule
// guards — the account's own guards and grants — are exactly the ones a
// stolen key must not reach.
func Reauthenticated(caller *jwtutils.Caller, _ Target) error {
	if caller == nil {
		return ErrUnauthenticated
	}
	if caller.IsMachine() {
		return ErrMachineCredential
	}
	if caller.SessionID == "" {
		return ErrUnauthenticated
	}
	if caller.IsImpersonating() {
		return ErrImpersonated
	}
	return nil
}

// ReauthenticationHeader carries the step-up proof a Reauthenticated
// procedure demands. The token is single use: the guarded call that reads it
// spends it, and a replay answers the same refusal an unknown token does.
const ReauthenticationHeader = "X-Saka-Reauthentication"

// ReauthConsumer spends a step-up proof. The guard defines the seam; the
// feature that mints the tokens implements it, and the transport's guard
// interceptor is built with the implementation the registry resolves. A nil
// consumer fails closed: a guarded procedure refuses rather than runs
// unproven.
type ReauthConsumer interface {
	ConsumeReauthentication(ctx context.Context, caller *jwtutils.Caller, token string) error
}

// RequiresReauthentication answers whether the procedure's table entry marks
// it as step-up guarded — the question the interceptor asks after the rule
// passes.
func RequiresReauthentication(procedure string) bool {
	entry, ok := ProcedureRules[procedure]
	return ok && entry.StepUp
}

// Permission answers a caller whose effective grants satisfy the requirement:
// a permission slug one of the caller's roles carries, or one granted to the
// account directly, matched with the wildcard the slug grammar allows in the
// instance position.
//
// requirement is a full slug, `resource:id:action` — the id the request is
// about when the rule is written for one instance, the wildcard when it is
// written for the kind. The grants are the claims' snapshot, so the rule
// stays a pure function of the request: a check that needed the database
// would belong in the service, and the guard runs before one.
func Permission(requirement string) Rule {
	return func(caller *jwtutils.Caller, _ Target) error {
		if caller == nil {
			return ErrUnauthenticated
		}
		if !caller.HasPermission(requirement) {
			return ErrPermissionRequired
		}
		return nil
	}
}

// StopImpersonating answers exactly the caller a delegation carries: a
// session credential whose claims name the administrator behind it. It is
// the one rule that requires the delegation rather than refusing it — the
// way out of a borrowed identity must not be closed by the refusals the
// other rules keep — and it still refuses a machine credential, which has
// no session to end and could never have opened a delegation.
func StopImpersonating(caller *jwtutils.Caller, _ Target) error {
	if caller == nil {
		return ErrUnauthenticated
	}
	if caller.IsMachine() {
		return ErrMachineCredential
	}
	if caller.SessionID == "" {
		return ErrUnauthenticated
	}
	if !caller.IsImpersonating() {
		return ErrImpersonated
	}
	return nil
}

// Self answers the account the request names, and nobody else.
//
// field is the name the identifier travels under: the proto field name of a
// procedure's request, or the path parameter of a REST route.
//
// An administrator does not pass by virtue of the role: a self-service
// request acts on the caller's own account, and an administrator who must act
// on another account has the administrative procedure for it. That is the
// rule upstream Pocket ID follows by construction — `/users/me` beside
// `/users/{id}` — expressed here as one rule instead of two routes.
//
// A caller that is impersonating is refused outright. The delegation exists
// so an administrator can act as another account through the administrative
// surface, not so it can use the requests that belong to the account.
func Self(field string) Rule {
	return func(caller *jwtutils.Caller, target Target) error {
		if caller == nil {
			return ErrUnauthenticated
		}
		if caller.IsImpersonating() {
			return ErrImpersonated
		}
		named, ok := target.ID(field)
		if !ok {
			// The contract does not carry the name the table declares. That
			// is a wiring defect, and it is refused rather than allowed.
			return ErrNotSelf
		}
		if !caller.ActsFor(named) {
			return ErrNotSelf
		}
		return nil
	}
}
