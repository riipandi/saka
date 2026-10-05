package password

import (
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"
)

// MinLength is the shortest credential the policy admits. Eight keeps the
// bar a human can clear while staying above the four-to-six ranges word
// lists and offline crackers make cheap to walk.
const MinLength = 8

// PassphraseLength is the length that stands in for character classes: a
// credential this long carries entropy no matter which classes it draws
// from, so a passphrase of plain words passes where a short run of one
// class ("12345678", "aaaaaaaa") fails.
const PassphraseLength = 16

// Validate judges a clear-text password against the account policy: at
// least MinLength runes, and either PassphraseLength runes or characters
// from three of the four classes.
//
// The classes are ASCII-letter upper and lower, digit, and everything else
// (spaces included): a password with a non-ASCII rune earns its class the
// same way a symbol does.
func Validate(clearText string) error {
	length := utf8.RuneCountInString(clearText)
	if length < MinLength {
		return fmt.Errorf("password: %w: at least %d characters", ErrWeakPassword, MinLength)
	}
	if length >= PassphraseLength {
		return nil
	}
	var upper, lower, digit, other bool
	for _, r := range clearText {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		default:
			other = true
		}
	}
	classes := 0
	for _, present := range []bool{upper, lower, digit, other} {
		if present {
			classes++
		}
	}
	if classes < 3 {
		return fmt.Errorf("password: %w: use characters from at least 3 of these groups: uppercase, lowercase, digits, symbols", ErrWeakPassword)
	}
	return nil
}

// ErrWeakPassword is the policy's refusal, wrapped by every failure Validate
// answers. The handlers match it — and not the wording — so a caller gets
// the argument's own text without the mapping learning the rules.
var ErrWeakPassword = errors.New("password does not meet the policy")
