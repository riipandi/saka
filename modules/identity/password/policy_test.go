package password

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/fetcher"

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

// corpusServer stands in for the range API: it answers the suffix the test
// hashed, records the request headers, and fails or stalls on demand. The
// real endpoint is unauthenticated — the stand-in asserts that, too.
type corpusServer struct {
	server  *httptest.Server
	checker *BreachChecker
	// prefixesSeen records the request path's hash prefix per call — the
	// k-anonymity fact: only five hex characters ever travel.
	prefixesSeen []string
	keySeen      string
	agentSeen    string
}

func serveCorpus(t *testing.T, answer func(prefix string) (int, string)) *corpusServer {
	t.Helper()
	captured := &corpusServer{}
	handler := func(w http.ResponseWriter, r *http.Request) {
		captured.keySeen = r.Header.Get("HIBP-Account-Key")
		captured.agentSeen = r.Header.Get("User-Agent")
		prefix := strings.TrimPrefix(r.URL.Path, "/range/")
		captured.prefixesSeen = append(captured.prefixesSeen, prefix)
		status, body := answer(prefix)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
	captured.server = httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(captured.server.Close)

	cfg := config.Default()
	cfg.Fetcher.RetryCount = 0
	cfg.Fetcher.Timeout = 2 * time.Second
	client, err := fetcher.New(cfg, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Shutdown(context.Background()) })

	captured.checker = NewBreachChecker(client, "", "saka-test")
	captured.checker.rangeURL = captured.server.URL + "/range/"
	return captured
}

func corpusAnswerFor(clearText string, count int) (string, string) {
	sum := sha1.Sum([]byte(clearText))
	hashed := strings.ToUpper(hex.EncodeToString(sum[:]))
	body := fmt.Sprintf("%s:%d\n", hashed[5:], count)
	// The real answer pads: hundreds of lines the suffix sits among.
	body += strings.Repeat("0000000000000000000000000000000000000000:1\n", 4)
	return hashed[:5], body
}

func TestTheCorpusRefusesAKnownBreachedPassword(t *testing.T) {
	const breached = "Passw0rd!2024"
	prefix, body := corpusAnswerFor(breached, 4)
	fixture := serveCorpus(t, func(string) (int, string) {
		return http.StatusOK, body
	})
	validator := NewValidator(stubPolicySettings{
		SettingRejectCompromised: "true",
	}, fixture.checker, nil)

	err := validator.Validate(t.Context(), breached)
	assert.ErrorIs(t, err, ErrBreachedPassword)

	// A credential the corpus never saw passes.
	assert.NoError(t, validator.Validate(t.Context(), "Vittoria-Vetra-1974"))

	// Only the five-character prefix of the hash ever travels: two calls,
	// the first the breached credential's, and both five hex characters.
	require.Len(t, fixture.prefixesSeen, 2)
	assert.Equal(t, prefix, fixture.prefixesSeen[0])
	assert.Len(t, fixture.prefixesSeen[1], 5)
}

func TestACorpusOutagePassesTheCredential(t *testing.T) {
	fixture := serveCorpus(t, func(string) (int, string) {
		return http.StatusInternalServerError, "upstream on fire"
	})
	validator := NewValidator(stubPolicySettings{
		SettingRejectCompromised: "true",
	}, fixture.checker, nil)

	// The refusal must not depend on the corpus: an outage is a warning,
	// never a failed sign-up.
	assert.NoError(t, validator.Validate(t.Context(), "Passw0rd!2024"))
}

func TestACorpusOutageFlagsNothingAtSignIn(t *testing.T) {
	fixture := serveCorpus(t, func(string) (int, string) {
		return http.StatusInternalServerError, "upstream on fire"
	})
	validator := NewValidator(stubPolicySettings{
		SettingRejectCompromised: "true",
	}, fixture.checker, nil)

	assert.False(t, validator.FlagsSignIn(t.Context(), "Passw0rd!2024"),
		"the sign-in must not fail over the corpus")
}

func TestASlowCorpusDoesNotStallTheGate(t *testing.T) {
	fixture := serveCorpus(t, func(string) (int, string) {
		time.Sleep(500 * time.Millisecond)
		return http.StatusOK, ""
	})
	fixture.checker.budget = 50 * time.Millisecond
	validator := NewValidator(stubPolicySettings{
		SettingRejectCompromised: "true",
	}, fixture.checker, nil)

	start := time.Now()
	err := validator.Validate(t.Context(), "Passw0rd!2024")
	elapsed := time.Since(start)

	assert.NoError(t, err, "the deadline trips the check, the caller fails open")
	assert.Less(t, elapsed, 400*time.Millisecond,
		"the budget, not the stalled upstream, bounds the wait")
}

func TestTheAccountKeyTravelsOnlyWhenConfigured(t *testing.T) {
	_, body := corpusAnswerFor("Passw0rd!2024", 4)
	fixture := serveCorpus(t, func(string) (int, string) {
		return http.StatusOK, body
	})

	// No key: the header stays off the wire — the range API needs none.
	_, err := fixture.checker.Count(t.Context(), "Passw0rd!2024")
	require.NoError(t, err)
	assert.Empty(t, fixture.keySeen)

	// A configured key rides along as a header, nothing more.
	fixture.checker.apiKey = "an-account-key"
	_, err = fixture.checker.Count(t.Context(), "Passw0rd!2024")
	require.NoError(t, err)
	assert.Equal(t, "an-account-key", fixture.keySeen)
	assert.NotEmpty(t, fixture.agentSeen, "the acceptable-use user agent travels")
}
