package appconfig

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheAuthCatalogIsWellFormed pins the invariants the new auth keys must
// hold: the keys are unique, nothing public rests sealed (the constructor
// checks that too, on the synthetic entries), and every default parses as
// the shape its key names — a broken default would ship broken behavior to
// every deployment that never overrides it.
func TestTheAuthCatalogIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	durations := map[string][2]int64{
		SettingSessionMaxLifetime:       {5 * 60, 10 * 365 * 24 * 60 * 60},
		SettingSessionInactivityTimeout: {5 * 60, 365 * 24 * 60 * 60},
	}
	booleans := []string{
		SettingAccessAllowlistEnabled,
		SettingAuthSignupEmailEnabled, SettingAuthRequireEmail,
		SettingAuthVerifyEmailAtSignup, SettingAuthSigninEmailEnabled,
		SettingAuthSigninEmailCodeEnabled, SettingAuthSignupUsernameEnabled,
		SettingAuthRequireUsername, SettingAuthSignupPasswordEnabled,
		SettingPasswordRejectCompromised, SettingPasswordRuleLowercase,
		SettingPasswordRuleUppercase, SettingPasswordRuleNumber,
		SettingPasswordRuleSpecial, SettingMFARequired,
		SettingUsersSelfDeleteEnabled, SettingUsersChangeEmailEnabled,
		SettingUsersChangeUsernameEnabled, SettingLockoutEnabled,
	}
	enums := map[string][]string{
		SettingAccessMode:                    {"open", "invite"},
		SettingAuthUserEnumerationProtection: {"bulk", "strict"},
	}

	for _, def := range Catalog() {
		assert.False(t, seen[def.Key], "catalog names %s twice", def.Key)
		seen[def.Key] = true
		assert.NotEmpty(t, def.Description, "%s: a catalog item describes itself", def.Key)

		if def.Public {
			assert.False(t, def.Sealed, "%s: public cannot rest sealed", def.Key)
		}

		bounds, isDuration := durations[def.Key]
		switch {
		case isDuration:
			value, err := strconv.ParseInt(def.Default, 10, 64)
			require.NoError(t, err, "%s: the default must be integer seconds", def.Key)
			assert.GreaterOrEqual(t, value, bounds[0], "%s: the default must clear the floor", def.Key)
			assert.LessOrEqual(t, value, bounds[1], "%s: the default must respect the ceiling", def.Key)
		case def.Key == SettingSessionReverificationWindow:
			// The spec names no ceiling; the bounds live with the
			// reader. Here the default only has to parse as positive
			// seconds.
			value, err := strconv.ParseInt(def.Default, 10, 64)
			require.NoError(t, err, "%s: the default must be integer seconds", def.Key)
			assert.Positive(t, value)
		case slices.Contains(booleans, def.Key):
			assert.Contains(t, []string{"true", "false"}, def.Default, "%s: the default must be a boolean", def.Key)
		case enums[def.Key] != nil:
			assert.Contains(t, enums[def.Key], def.Default, "%s: the default must be one of the allowed values", def.Key)
		case def.Key == SettingPasswordMinLength:
			value, err := strconv.Atoi(def.Default)
			require.NoError(t, err, "%s: the default must be an integer", def.Key)
			assert.GreaterOrEqual(t, value, 8, "the floor the tests pin the policy to")
		case def.Key == SettingLockoutMaxAttempts:
			value, err := strconv.Atoi(def.Default)
			require.NoError(t, err, "%s: the default must be an integer", def.Key)
			assert.GreaterOrEqual(t, value, 5, "the reader's floor")
		case def.Key == SettingLockoutDuration:
			if def.Default != "" {
				_, err := time.ParseDuration(def.Default)
				require.NoError(t, err, "%s: the default must be a duration or empty", def.Key)
			}
		case def.Key == SettingAccessAllowlist:
			assert.Equal(t, "", def.Default, "the allowlist ships empty; an operator fills it")
		}
	}

	// The names the later phases read are spelled exactly once — the
	// catalog is the contract, and a misspelled reader is a silent default.
	for _, key := range []string{
		SettingAccessMode, SettingAccessAllowlist, SettingAccessAllowlistEnabled,
		SettingAuthSignupEmailEnabled, SettingAuthRequireEmail,
		SettingAuthVerifyEmailAtSignup, SettingAuthSigninEmailEnabled,
		SettingAuthSigninEmailCodeEnabled, SettingAuthSignupUsernameEnabled,
		SettingAuthRequireUsername, SettingAuthSignupPasswordEnabled,
		SettingPasswordMinLength, SettingPasswordRejectCompromised,
		SettingPasswordRuleLowercase,
		SettingPasswordRuleUppercase, SettingPasswordRuleNumber,
		SettingPasswordRuleSpecial, SettingMFARequired,
		SettingAuthUserEnumerationProtection,
		SettingLockoutEnabled, SettingLockoutMaxAttempts, SettingLockoutDuration,
		SettingSessionMaxLifetime, SettingSessionInactivityTimeout,
		SettingSessionReverificationWindow, SettingUsersSelfDeleteEnabled,
		SettingUsersChangeEmailEnabled, SettingUsersChangeUsernameEnabled,
	} {
		assert.True(t, seen[key], "the catalog must declare %s", key)
	}
}

// TestThePublicFamilyStaysPreAuthSmall pins the boundary the unauthenticated
// read answers: the mode and the allowlist gate, the identity toggles the
// sign-up and sign-in screens need before any account exists, the factor
// limits the enrollment screens show, and the account toggles the account
// screens gate their own actions by (self-delete, email change, username
// change). A key that leaks policy detail past that set either loses its
// Public flag or gains a deliberate exception here, with a reason.
func TestThePublicFamilyStaysPreAuthSmall(t *testing.T) {
	var public []string
	for _, def := range Catalog() {
		if def.Public {
			public = append(public, def.Key)
		}
	}

	expected := []string{
		SettingMFAMaxEnrollments,
		SettingPasskeyMaxCredentials,
		SettingAccessMode,
		SettingAuthSignupEmailEnabled,
		SettingAuthSigninEmailEnabled,
		SettingAuthSigninEmailCodeEnabled,
		SettingAuthSignupUsernameEnabled,
		SettingAuthRequireUsername,
		SettingAuthSignupPasswordEnabled,
		SettingUsersSelfDeleteEnabled,
		SettingUsersChangeEmailEnabled,
		SettingUsersChangeUsernameEnabled,
	}
	require.Len(t, public, len(expected))
	assert.ElementsMatch(t, expected, public)
}
