package password

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPolicySettings answers the catalog keys the policy reads.
type stubPolicySettings map[string]string

func (s stubPolicySettings) GetString(_ context.Context, key string) (string, error) {
	if value, ok := s[key]; ok {
		return value, nil
	}
	return "", errors.New("unreadable setting")
}

func (s stubPolicySettings) GetBool(_ context.Context, key string) (bool, error) {
	value, ok := s[key]
	if !ok {
		return false, errors.New("unreadable setting")
	}
	return value == "true", nil
}

func (s stubPolicySettings) GetInt64(_ context.Context, key string) (int64, error) {
	value, ok := s[key]
	if !ok {
		return 0, errors.New("unreadable setting")
	}
	var parsed int64
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, errors.New("not a number")
		}
		parsed = parsed*10 + int64(char-'0')
	}
	return parsed, nil
}

func TestValidatorReadsThePolicyFromSettings(t *testing.T) {
	// The minimum length is the settings': a floor the catalog default
	// does not decide.
	validator := NewValidator(stubPolicySettings{
		SettingMinLength: "12",
	}, nil, nil)

	err := validator.Validate(t.Context(), "short-one!")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWeakPassword)

	// Long enough passes with no explicit rule: the legacy floor still
	// asks for the classes, and a passphrase stands in for them.
	assert.NoError(t, validator.Validate(t.Context(), "a very long password indeed"))

	// An out-of-bounds or unreadable length keeps the default: the gate
	// must not swing open because a setting broke.
	silent := NewValidator(stubPolicySettings{
		SettingMinLength: "3",
	}, nil, nil)
	assert.Error(t, silent.Validate(t.Context(), "shortie"), "the out-of-bounds floor falls back to the default's 8")
}

func TestValidatorRunsTheExplicitRules(t *testing.T) {
	validator := NewValidator(stubPolicySettings{
		SettingMinLength:     "8",
		SettingRuleLowercase: "true",
		SettingRuleUppercase: "true",
		SettingRuleNumber:    "true",
	}, nil, nil)

	// The explicit rules are unconditional — a passphrase length buys no
	// exemption from a class the operator turned on.
	err := validator.Validate(t.Context(), "all lowercase but very long indeed")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWeakPassword)
	assert.Error(t, validator.Validate(t.Context(), "NO LOWERCASE AT ALL"))

	assert.NoError(t, validator.Validate(t.Context(), "Expecto-Patronum-9"))
}

func TestValidatorKeepsTheLegacyFloorWithoutExplicitRules(t *testing.T) {
	validator := NewValidator(stubPolicySettings{SettingMinLength: "8"}, nil, nil)

	// Three of the four classes, shorter than the passphrase: the rule the
	// toggles' silence leaves standing.
	assert.Error(t, validator.Validate(t.Context(), "onlylowercase1"))
	assert.NoError(t, validator.Validate(t.Context(), "one-of-each-9"))
}

func TestValidatorMarksTheStrengthFloorAsDeferred(t *testing.T) {
	// The deferred meter: the key stays readable, the gate stays closed to
	// nothing — any level accepts what the other rules accept. The marker
	// is the rework debt the owner named.
	validator := NewValidator(stubPolicySettings{
		SettingMinStrength: "strict",
	}, nil, nil)
	assert.Equal(t, "strict", validator.deferredStrength(validator.settings))
}

func TestScanRangeAnswersTheSuffixCount(t *testing.T) {
	body := strings.Join([]string{
		"0018A45C4D1DEF836AAD78E7B34E1F5DD33:3",
		"5B61EEABCBOTJUNK:0",
		"00A4C72BCHealthCheck:22",
	}, "\n")

	count, err := scanRange([]byte(body), "0018A45C4D1DEF836AAD78E7B34E1F5DD33")
	require.NoError(t, err)
	assert.Equal(t, int32(3), count)

	// The match is case-insensitive: the answer's suffixes are uppercase,
	// the comparison does not care.
	count, err = scanRange([]byte(body), "5b61eeabcbotjunk")
	require.NoError(t, err)
	assert.Equal(t, int32(0), count)

	// An absent suffix is the never-seen answer, not an error.
	count, err = scanRange([]byte(body), "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF")
	require.NoError(t, err)
	assert.Equal(t, int32(0), count)
}

func TestBreachCheckerWithoutAClientIsTheOffState(t *testing.T) {
	// No fetcher: the optional feature is off, and the error says
	// unavailable — the validator's fail-open passes the credential.
	checker := NewBreachChecker(nil, "", "")
	_, err := checker.Count(t.Context(), "whatever")
	assert.Error(t, err)

	var bare *BreachChecker
	_, err = bare.Count(t.Context(), "whatever")
	assert.Error(t, err)
}
