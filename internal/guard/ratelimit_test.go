package guard_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authnv1connect "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1/authnv1connect"
	identityv1connect "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1/identityv1connect"
	"github.com/riipandi/tango/internal/guard"
)

// TestEveryPublicProcedureIsClassified keeps the counted surface a decision:
// a procedure the guard answers without a credential is either throttled (in
// one of the two buckets) or named exempt — a public procedure in neither
// table is a gap the limiter never saw, so the test fails instead.
func TestEveryPublicProcedureIsClassified(t *testing.T) {
	for procedure := range guard.PublicProcedures() {
		_, counted := guard.RateBucketFor(procedure)
		if counted {
			continue
		}
		_, exempt := guard.RateExemptProcedures[procedure]
		assert.True(t, exempt,
			"the public procedure %s is in neither the buckets nor the exemption", procedure)
	}
}

// TestTheRateTablesNameRealProcedures keeps a renamed or deleted procedure
// from leaving a stale policy behind: an entry that names nothing is a rule
// nothing ever reaches, the same defect the authorization tables' own
// derived check refuses.
func TestTheRateTablesNameRealProcedures(t *testing.T) {
	known := map[string]struct{}{}
	for _, procedure := range guard.ContractProcedures() {
		known[procedure] = struct{}{}
	}

	for table, entries := range map[string]map[string]struct{}{
		"RateAuthProcedures":    guard.RateAuthProcedures,
		"RateDefaultProcedures": guard.RateDefaultProcedures,
		"RateExemptProcedures":  guard.RateExemptProcedures,
	} {
		for procedure := range entries {
			_, ok := known[procedure]
			require.True(t, ok, "%s names %q, which no contract declares", table, procedure)
		}
	}
}

// TestTheCredentialBucketCarriesTheAttemptSurface pins the bucket's shape:
// the public procedures a script can drive at a credential or a code, and
// the senders that can provoke the mailer, all count against the tight
// budget the configuration gives the credential bucket.
func TestTheCredentialBucketCarriesTheAttemptSurface(t *testing.T) {
	for _, procedure := range []string{
		authnv1connect.AuthServiceSignInProcedure,
		identityv1connect.SignupServiceSignupProcedure,
		authnv1connect.OneTimeAccessServiceExchangeTokenProcedure,
		authnv1connect.MultifactorServiceCompleteSignInProcedure,
		authnv1connect.PasswordRecoveryServiceForgotPasswordProcedure,
		identityv1connect.EmailVerificationServiceSendEmailProcedure,
	} {
		bucket, counted := guard.RateBucketFor(procedure)
		require.True(t, counted, "%s must be counted", procedure)
		assert.Equal(t, guard.RateAuth, bucket,
			"%s must count against the credential bucket", procedure)
	}
}
