package guard_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identityv1 "github.com/riipandi/saka/codegen/proto/go/saka/identity/v1"
	"github.com/riipandi/saka/internal/guard"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// caller builds the principal a request runs as: the account the token names,
// with or without the delegation an impersonated session carries.
func caller(userID string, admin bool) *jwtutils.Caller {
	return &jwtutils.Caller{
		UserID:       userID,
		AccessClaims: jwtutils.AccessClaims{Username: "hermione", Roles: adminRoles(admin)},
	}
}

// adminRoles spells the claim an administrator's token carries, and the
// empty set a non-administrator's does not.
func adminRoles(admin bool) []string {
	if admin {
		return []string{jwtutils.AdministratorRole}
	}
	return nil
}

// impersonator builds an administrator's delegated session: the token acts as
// the subject while naming the administrator behind it.
func impersonator(subject string) *jwtutils.Caller {
	return &jwtutils.Caller{
		UserID: subject,
		AccessClaims: jwtutils.AccessClaims{
			Username:      "hermione",
			Roles:         adminRoles(true),
			ActorID:       "01a0da1c-cb41-779d-bd02-99b3eb5da32a",
			ActorUsername: "admin",
		},
	}
}

func TestPublicAnswersEveryCaller(t *testing.T) {
	for name, c := range map[string]*jwtutils.Caller{"anonymous": nil, "caller": caller("01a0", false)} {
		assert.NoError(t, guard.Public(c, guard.Target{}), name)
	}
}

func TestAuthenticatedAnswersAnyVerifiedCaller(t *testing.T) {
	// The rule is for a procedure that takes no target from the request: the
	// account is the claims' subject, so the role is not the question.
	assert.ErrorIs(t, guard.Authenticated(nil, guard.Target{}), guard.ErrUnauthenticated)
	assert.NoError(t, guard.Authenticated(caller("01a0", false), guard.Target{}))
	assert.NoError(t, guard.Authenticated(caller("01a0", true), guard.Target{}))
}

// TestAuthenticatedRefusesADelegatedCaller pins the half that makes the rule
// usable for a self-service procedure with no target in the request — the
// audit trail's own listing. The account is the claims' subject, so there is
// no identifier to compare; the delegation is the only thing left to refuse
// on, and an account's own history is exactly what a delegation must not read
// as if it were the account.
func TestAuthenticatedRefusesADelegatedCaller(t *testing.T) {
	delegated := caller("01a0", false)
	delegated.ActorID = "01a0da1c-cb41-779d-bd02-99b3eb5da32a"
	delegated.ActorUsername = "admin"

	assert.ErrorIs(t, guard.Authenticated(delegated, guard.Target{}), guard.ErrImpersonated)
	// The role does not lift it: an administrator who is impersonating is
	// still acting as somebody else.
	adminDelegated := caller("01a0", true)
	adminDelegated.ActorID = "01a0da1c-cb41-779d-bd02-99b3eb5da32a"
	assert.ErrorIs(t, guard.Authenticated(adminDelegated, guard.Target{}), guard.ErrImpersonated)
}

func TestAdminAnswersOnlyAnAdministrator(t *testing.T) {
	assert.ErrorIs(t, guard.Admin(nil, guard.Target{}), guard.ErrUnauthenticated)
	assert.ErrorIs(t, guard.Admin(caller("01a0", false), guard.Target{}), guard.ErrAdminRequired)
	assert.NoError(t, guard.Admin(caller("01a0", true), guard.Target{}))
}

// TestSelfAnswersOnlyTheNamedAccount is the rule's whole point: the account
// the request names must be the account the caller is.
func TestSelfAnswersOnlyTheNamedAccount(t *testing.T) {
	rule := guard.Self("id")
	message := &identityv1.ResetProfilePictureRequest{Id: "01a0da1c-cb41-779d-bd02-99b3eb5da32a"}

	assert.ErrorIs(t, rule(nil, guard.Target{Message: message}), guard.ErrUnauthenticated)
	assert.NoError(t, rule(caller(message.Id, false), guard.Target{Message: message}))
	assert.ErrorIs(t, rule(caller("01a0da1c-0000-7000-8000-000000000000", false), guard.Target{Message: message}), guard.ErrNotSelf)
}

// TestSelfRefusesAnAdministratorActingOnAnotherAccount keeps the rule
// upstream reaches by construction: `/users/me` beside `/users/{id}`. The
// role is not a way past the self-service request.
func TestSelfRefusesAnAdministratorActingOnAnotherAccount(t *testing.T) {
	rule := guard.Self("id")
	message := &identityv1.ResetProfilePictureRequest{Id: "01a0da1c-cb41-779d-bd02-99b3eb5da32a"}

	assert.ErrorIs(t, rule(caller("01a0da1c-0000-7000-8000-000000000000", true), guard.Target{Message: message}), guard.ErrNotSelf)
}

// TestSelfRefusesAnImpersonatedCaller covers the delegation: an
// administrator's session that acts as another account reaches the
// administrative surface, never the requests that belong to the account.
func TestSelfRefusesAnImpersonatedCaller(t *testing.T) {
	rule := guard.Self("id")
	message := &identityv1.ResetProfilePictureRequest{Id: "01a0da1c-cb41-779d-bd02-99b3eb5da32a"}

	// The impersonated caller is the very account the request names, and is
	// still refused: the delegation itself is the disqualifier.
	assert.ErrorIs(t,
		rule(impersonator(message.Id), guard.Target{Message: message}),
		guard.ErrImpersonated)
}

// TestSelfRefusesARequestItCannotCompare keeps the guard failing closed: a
// name the request does not carry is a wiring defect, and a guard that cannot
// find what it compares must not answer "yes".
func TestSelfRefusesARequestItCannotCompare(t *testing.T) {
	rule := guard.Self("id")

	// No message at all, and a message whose identifier is empty: neither
	// names an account, so neither can match the caller.
	assert.ErrorIs(t, rule(caller("01a0", false), guard.Target{}), guard.ErrNotSelf)
	assert.ErrorIs(t, rule(caller("01a0", false), guard.Target{Message: &identityv1.ResetProfilePictureRequest{}}), guard.ErrNotSelf)

	// A message of another procedure: the field the table names is not in
	// this contract, which is what the table test catches at build time.
	assert.ErrorIs(t, rule(caller("01a0", false), guard.Target{Message: &identityv1.ListUsersRequest{}}), guard.ErrNotSelf)
}

// TestSelfAnswersTheMessageThatNamesTheCaller is the allowed half of the
// same comparison, including a field another procedure also declares: the
// rule reads the message it was handed, not the one the table was written
// for.
func TestSelfAnswersTheMessageThatNamesTheCaller(t *testing.T) {
	rule := guard.Self("id")

	assert.NoError(t, rule(caller("01a0", false), guard.Target{Message: &identityv1.ResetProfilePictureRequest{Id: "01a0"}}))
	assert.NoError(t, rule(caller("01a0", false), guard.Target{Message: &identityv1.GetUserRequest{Id: "01a0"}}))
}

// TestSelfReadsAPathParameter covers the REST half: the same rule reads the
// identifier from the route's path when the request is not a message.
func TestSelfReadsAPathParameter(t *testing.T) {
	rule := guard.Self("id")
	target := guard.Target{PathParams: map[string]string{"id": "01a0"}}

	assert.NoError(t, rule(caller("01a0", false), target))
	assert.ErrorIs(t, rule(caller("01a1", false), target), guard.ErrNotSelf)
}

// TestRuleForDefaultsToAdministrative is the safe default: a procedure the
// table does not name refuses a caller without the role, so a new procedure
// is protected the moment it is mounted.
func TestRuleForDefaultsToAdministrative(t *testing.T) {
	rule := guard.RuleFor("/saka.unknown.v1.UnknownService/Absent")
	require.NotNil(t, rule)

	assert.ErrorIs(t, rule(caller("01a0", false), guard.Target{}), guard.ErrAdminRequired)
	assert.NoError(t, rule(caller("01a0", true), guard.Target{}))
}

// TestMatchRestDefaultsToAdministrative is the same default on the REST
// surface, and it also keeps a pattern from matching a path of another shape.
func TestMatchRestDefaultsToAdministrative(t *testing.T) {
	rule, _ := guard.MatchRest(nil, "GET", "/api/anything")
	assert.ErrorIs(t, rule(caller("01a0", false), guard.Target{}), guard.ErrAdminRequired)

	// The picture read is public and its pattern spans three segments; a
	// longer path is a different route.
	rule, _ = guard.MatchRest(guard.RestRules, "GET", "/api/users/01a0/extra/profile-picture.png")
	assert.ErrorIs(t, rule(caller("01a0", false), guard.Target{}), guard.ErrAdminRequired)
}

// TestMatchRestReadsThePathParameter keeps the REST self route working: the
// parameter comes from the position the pattern puts it in.
func TestMatchRestReadsThePathParameter(t *testing.T) {
	rule, target := guard.MatchRest(guard.RestRules, "PUT", "/api/users/01a0da1c-cb41-779d-bd02-99b3eb5da32a/profile-picture")

	assert.NoError(t, rule(caller("01a0da1c-cb41-779d-bd02-99b3eb5da32a", false), target))
	assert.ErrorIs(t, rule(caller("01a0da1c-0000-7000-8000-000000000000", false), target), guard.ErrNotSelf)
}

// TestMatchRestCarriesTheTusRoutes pins the uploads' protocol surface: every
// method the tus client speaks is authenticated (any signed-in account may
// upload), the catch-all absorbs the bucket-scoped reference a HEAD, PATCH,
// or DELETE names, and a method the protocol does not define on the prefix
// is administrative — an unnamed route defaults, it does not slip through.
func TestMatchRestCarriesTheTusRoutes(t *testing.T) {
	// The creation sits on the bare prefix and demands a caller; the
	// discovery does not — it names what a client may rely on before it
	// holds a credential.
	for _, method := range []string{"OPTIONS"} {
		rule, _ := guard.MatchRest(guard.RestRules, method, "/api/uploads")
		assert.NoError(t, rule(nil, guard.Target{}), method)
	}
	rule, _ := guard.MatchRest(guard.RestRules, "POST", "/api/uploads")
	assert.NoError(t, rule(caller("01a0", false), guard.Target{}))
	assert.ErrorIs(t, rule(nil, guard.Target{}), guard.ErrUnauthenticated)

	// The session's own methods sit on the bucket-scoped reference; the
	// discovery answers without a credential, the rest do not.
	for _, method := range []string{"HEAD", "PATCH", "DELETE"} {
		rule, target := guard.MatchRest(guard.RestRules, method, "/api/uploads/pictures/01a0da1c-cb41-779d-bd02-99b3eb5da32a.png")
		assert.NoError(t, rule(caller("01a0", false), target), method)
		assert.ErrorIs(t, rule(nil, target), guard.ErrUnauthenticated, method)
	}

	// A method the protocol does not define defaults to administrative.
	getRule, _ := guard.MatchRest(guard.RestRules, "GET", "/api/uploads/pictures/01a0da1c.png")
	assert.ErrorIs(t, getRule(caller("01a0", false), guard.Target{}), guard.ErrAdminRequired)
}

// TestPublicProceduresComeFromTheTable pins the derivation: the set the
// authenticator excuses is read from the rules, so a procedure cannot be
// public for one seam and guarded for the other.
func TestPublicProceduresComeFromTheTable(t *testing.T) {
	public := guard.PublicProcedures()
	require.NotEmpty(t, public)

	for procedure, entry := range guard.ProcedureRules {
		if guard.IsPublic(entry.Rule) {
			assert.Contains(t, public, procedure)
			continue
		}
		assert.NotContains(t, public, procedure, procedure)
	}
}

// TestEverySelfDeclarationNamesARealField keeps the table checked against the
// contract: a declaration naming a field no message carries is a guard that
// can never pass, and it must fail here rather than in production.
func TestEverySelfDeclarationNamesARealField(t *testing.T) {
	for procedure, entry := range guard.ProcedureRules {
		if entry.Field == "" {
			assert.Nil(t, entry.Prototype, procedure)
			continue
		}
		require.NotNil(t, entry.Prototype, procedure)
		require.NoError(t, guard.FieldExists(entry.Prototype, entry.Field), procedure)
	}
}

// TestEveryDeclaredProcedureExistsInTheContract keeps the table from drifting
// away from the surface: a path the contract no longer declares is a rule
// attached to nothing.
func TestEveryDeclaredProcedureExistsInTheContract(t *testing.T) {
	for procedure := range guard.ProcedureRules {
		assert.Contains(t, guard.ContractProcedures(), procedure)
	}
}

// TestStopImpersonatingRuleRequiresTheDelegation covers the one rule that
// must admit an impersonated caller: a session credential carrying an actor
// pair passes, a machine credential and a plain session are refused, and a
// caller without a session is refused as unauthenticated.
func TestStopImpersonatingRuleRequiresTheDelegation(t *testing.T) {
	delegated := &jwtutils.Caller{
		UserID: "user_01a0da1ccb4177900000000000",
		AccessClaims: jwtutils.AccessClaims{
			Username:  "sophie_neveu",
			SessionID: "sess_01a0da1ccb4177900000000000",
			ActorID:   "user_01a0da1ccb4177900000000001",
		},
	}
	assert.NoError(t, guard.StopImpersonating(delegated, guard.Target{}))

	plain := &jwtutils.Caller{
		UserID: "user_01a0da1ccb4177900000000000",
		AccessClaims: jwtutils.AccessClaims{
			Username:  "sophie_neveu",
			SessionID: "sess_01a0da1ccb4177900000000000",
		},
	}
	assert.ErrorIs(t, guard.StopImpersonating(plain, guard.Target{}), guard.ErrImpersonated)

	machine := &jwtutils.Caller{
		UserID: "user_01a0da1ccb4177900000000000",
		AccessClaims: jwtutils.AccessClaims{
			Username:  "sophie_neveu",
			SessionID: "sess_01a0da1ccb4177900000000000",
			ActorID:   "user_01a0da1ccb4177900000000001",
		},
		Credential: jwtutils.CredentialAPIKey,
	}
	assert.ErrorIs(t, guard.StopImpersonating(machine, guard.Target{}), guard.ErrMachineCredential)

	assert.ErrorIs(t, guard.StopImpersonating(nil, guard.Target{}), guard.ErrUnauthenticated)
}
