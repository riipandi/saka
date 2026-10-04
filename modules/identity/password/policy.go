package password

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/riipandi/saka/framework/fetcher"
)

// The catalog keys the policy reads. The catalog owns the names; these
// constants are how this package spells them.
const (
	SettingMinLength         = "password.min_length"
	SettingRejectCompromised = "password.reject_compromised"
	SettingRuleLowercase     = "password.rule_lowercase"
	SettingRuleUppercase     = "password.rule_uppercase"
	SettingRuleNumber        = "password.rule_number"
	SettingRuleSpecial       = "password.rule_special"
)

// The explicit-rule ceiling: a settings value outside the sane range is a
// misconfiguration, and the default answers instead of it.
const (
	minLengthFloor   = 1
	minLengthCeil    = 128
	defaultMinLength = MinLength
)

// ErrBreachedPassword is the policy's refusal for a credential the breach
// corpus knows. The handlers match it with the weak-password one — the wire
// form is the same invalid-argument refusal, never the corpus's count.
var ErrBreachedPassword = errors.New("password has appeared in a known data breach")

// SettingsReader is the policy's runtime source: the catalog read fresh at
// every check, so an operator's change lands on the next attempt.
// *appconfig.Settings satisfies it; the interface keeps the appconfig
// feature out of this one's import graph.
type SettingsReader interface {
	GetString(ctx context.Context, key string) (string, error)
	GetBool(ctx context.Context, key string) (bool, error)
	GetInt64(ctx context.Context, key string) (int64, error)
}

// Validator judges a clear-text password against the settings' policy: the
// minimum length, the explicit character-class rules, the legacy
// three-of-four-classes floor when no explicit rule is on, and the breach
// corpus where the toggle arms it. Nil keeps the static Validate — the
// state a bare wiring is in.
type Validator struct {
	settings SettingsReader
	breach   *BreachChecker
	log      *slog.Logger
}

// NewValidator builds the settings-driven policy. Either source may be nil:
// no settings keeps the built-in defaults, no breach checker keeps the
// corpus check off.
func NewValidator(settings SettingsReader, breach *BreachChecker, log *slog.Logger) *Validator {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Validator{settings: settings, breach: breach, log: log}
}

// rules is the resolved policy one check runs against.
type rules struct {
	minLength         int
	ruleLowercase     bool
	ruleUppercase     bool
	ruleNumber        bool
	ruleSpecial       bool
	rejectCompromised bool
}

// resolve reads the catalog. An unreadable or out-of-bounds key keeps its
// catalog default: a settings row that cannot answer must not weaken or
// harden the gate silently.
func (v *Validator) resolve(ctx context.Context) rules {
	r := rules{minLength: defaultMinLength}
	if v.settings == nil {
		return r
	}
	if length, err := v.settings.GetInt64(ctx, SettingMinLength); err != nil {
		v.log.WarnContext(ctx, "password: min_length unreadable; using the default", "error", err)
	} else if length < minLengthFloor || length > minLengthCeil {
		v.log.WarnContext(ctx, "password: min_length out of bounds; using the default", "value", length)
	} else {
		r.minLength = int(length)
	}
	for _, bound := range []struct {
		key    string
		target *bool
	}{
		{SettingRejectCompromised, &r.rejectCompromised},
		{SettingRuleLowercase, &r.ruleLowercase},
		{SettingRuleUppercase, &r.ruleUppercase},
		{SettingRuleNumber, &r.ruleNumber},
		{SettingRuleSpecial, &r.ruleSpecial},
	} {
		if on, err := v.settings.GetBool(ctx, bound.key); err != nil {
			v.log.WarnContext(ctx, "password: setting unreadable; using the default", "key", bound.key, "error", err)
		} else {
			*bound.target = on
		}
	}
	return r
}

// Validate judges a clear-text password against the settings' policy.
//
// The explicit rules are unconditional: a rule the operator turned on is a
// character class the credential must carry, whatever its length. When no
// explicit rule is on, the legacy floor applies — PassphraseLength runes
// stand in for the classes, and shorter credentials draw from three of the
// four classes (spaces included; a non-ASCII rune earns its class the same
// way a symbol does). The breach corpus is the last check, only where the
// toggle arms it and the checker is available; a corpus that cannot answer
// never refuses.
func (v *Validator) Validate(ctx context.Context, clearText string) error {
	r := v.resolve(ctx)

	length := utf8.RuneCountInString(clearText)
	if length < r.minLength {
		return fmt.Errorf("password: %w: at least %d characters", ErrWeakPassword, r.minLength)
	}

	var hasLower, hasUpper, hasDigit, hasOther bool
	for _, char := range clearText {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsDigit(char):
			hasDigit = true
		default:
			hasOther = true
		}
	}
	for _, bound := range []struct {
		required bool
		carried  bool
		name     string
	}{
		{r.ruleLowercase, hasLower, "a lowercase letter"},
		{r.ruleUppercase, hasUpper, "an uppercase letter"},
		{r.ruleNumber, hasDigit, "a digit"},
		{r.ruleSpecial, hasOther, "a symbol"},
	} {
		if bound.required && !bound.carried {
			return fmt.Errorf("password: %w: must carry %s", ErrWeakPassword, bound.name)
		}
	}

	// The legacy floor runs only where no explicit rule speaks: an operator
	// naming classes has already said what the credential must carry.
	if !r.ruleLowercase && !r.ruleUppercase && !r.ruleNumber && !r.ruleSpecial {
		if length < PassphraseLength {
			classes := 0
			for _, present := range []bool{hasUpper, hasLower, hasDigit, hasOther} {
				if present {
					classes++
				}
			}
			if classes < 3 {
				return fmt.Errorf("password: %w: use characters from at least 3 of these groups: uppercase, lowercase, digits, symbols, or make it at least %d characters long", ErrWeakPassword, PassphraseLength)
			}
		}
	}

	if r.rejectCompromised {
		breached, err := v.Breached(ctx, clearText)
		if err != nil {
			// The corpus is fail-open: an unreadable answer never refuses
			// the credential, and the warning is the audit trail's hint.
			v.log.WarnContext(ctx, "password: breach check unavailable; passing", "error", err)
		} else if breached {
			return ErrBreachedPassword
		}
	}
	return nil
}

// Breached answers whether the corpus knows the credential — the sign-up
// path's question, where the answer refuses. An unavailable checker answers
// false: the check may miss, the refusal must not depend on the corpus.
func (v *Validator) Breached(ctx context.Context, clearText string) (bool, error) {
	if v.breach == nil {
		return false, nil
	}
	count, err := v.breach.Count(ctx, clearText)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// FlagsSignIn answers whether a breached credential should be flagged at
// sign-in: the toggle arms it, the corpus answers it, and an unavailable
// answer flags nothing — the sign-in must not fail over the corpus.
func (v *Validator) FlagsSignIn(ctx context.Context, clearText string) bool {
	if v.breach == nil || v.settings == nil {
		return false
	}
	if on, err := v.settings.GetBool(ctx, SettingRejectCompromised); err != nil || !on {
		return false
	}
	flagged, err := v.Breached(ctx, clearText)
	if err != nil {
		v.log.WarnContext(ctx, "password: breach check unavailable at sign-in; passing", "error", err)
		return false
	}
	return flagged
}

// BreachChecker is the breach corpus behind the validator: the Pwned
// Passwords range API over the process's one outbound client. The credential
// never leaves the process in the clear — the range API speaks k-anonymity,
// so only the first five characters of its SHA-1 hash travel, over a padded
// answer. The corpus is where the fetcher's circuit breaker earns its keep:
// a dead upstream opens the gate instead of stalling sign-ups.
type BreachChecker struct {
	fetch *fetcher.Client
	// apiKey is the account key the range API never asks for; it travels
	// only when a deployment configured it, for the authenticated headers
	// an account-level deployment answers with.
	apiKey string
	// userAgent is the deployment's product token, the acceptable-use rule.
	userAgent string
	// rangeURL is the corpus endpoint. The production value is the const;
	// the field exists so a test can answer from its own server.
	rangeURL string
	// budget bounds one corpus call. A dead upstream must cost the caller
	// seconds, not a sign-up-length stall: the deadline trips, the caller
	// fails open, and the breaker takes over the fast-failing after that.
	budget time.Duration
}

// The range API's endpoint. The range path is the SHA-1 prefix; the body
// answers the suffixed counts.
const breachRangeURL = "https://api.pwnedpasswords.com/range/"

// breachCheckBudget is the default one-call deadline. The range API answers
// in well under a second when it is healthy; anything longer reads as an
// outage, and the caller's fail-open is cheaper than the wait.
const breachCheckBudget = 5 * time.Second

// NewBreachChecker builds the corpus checker over the shared outbound
// client. A nil fetcher is the optional feature's off state — the checker
// answers unavailable without dialling anything. The account key is not a
// switch: the range API is unauthenticated, so an empty key only means the
// header stays off the wire.
func NewBreachChecker(fetch *fetcher.Client, apiKey, userAgent string) *BreachChecker {
	if fetch == nil {
		return &BreachChecker{}
	}
	checker := &BreachChecker{
		fetch:    fetch,
		apiKey:   apiKey,
		rangeURL: breachRangeURL,
		budget:   breachCheckBudget,
	}
	if userAgent != "" {
		checker.userAgent = userAgent
	}
	return checker
}

// Count answers how many times the corpus has seen the credential. A checker
// with no client is the off state: the error says "unavailable", never
// "breached", and the caller fails open. The call rides the checker's
// budget, not the caller's patience: retries and backoff live under this
// deadline, so the worst an unreachable corpus costs is the budget itself.
func (b *BreachChecker) Count(ctx context.Context, clearText string) (int32, error) {
	if b == nil || b.fetch == nil {
		return 0, errors.New("password: no breach checker is configured")
	}

	sum := sha1.Sum([]byte(clearText))
	hashed := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := hashed[:5], hashed[5:]

	headers := http.Header{}
	if b.userAgent != "" {
		headers.Set("User-Agent", b.userAgent)
	}
	if b.apiKey != "" {
		headers.Set("HIBP-Account-Key", b.apiKey)
	}

	callCtx, cancel := context.WithTimeout(ctx, b.budget)
	defer cancel()

	res, err := b.fetch.Do(callCtx, fetcher.Request{
		Method:  http.MethodGet,
		URL:     b.rangeURL + prefix,
		Headers: headers,
	})
	if err != nil {
		return 0, fmt.Errorf("password: breach corpus: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("password: breach corpus: the range API answered %d", res.StatusCode)
	}
	return scanRange(res.Body, suffix)
}

// scanRange reads the range answer's lines — `SUFFIX:COUNT`, most often
// hundreds of them — and answers the count the credential's suffix carries.
// A suffix absent from the padded answer means the corpus has never seen it.
func scanRange(body []byte, suffix string) (int32, error) {
	for line := range strings.SplitSeq(string(body), "\n") {
		entry, count, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || !strings.EqualFold(entry, suffix) {
			continue
		}
		value, err := strconv.ParseInt(strings.TrimSpace(count), 10, 32)
		if err != nil {
			return 0, fmt.Errorf("password: breach corpus: the count %q does not parse", count)
		}
		return int32(value), nil
	}
	return 0, nil
}
