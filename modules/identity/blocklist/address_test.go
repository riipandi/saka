package blocklist

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidatePatternAcceptsTheGrammar pins the stored form: lowercased,
// trimmed, one address or one @domain entry — the shapes the column's check
// constraint reads again at the write.
func TestValidatePatternAcceptsTheGrammar(t *testing.T) {
	valid := map[string]string{
		"  John.Doe@Example.com ": "john.doe@example.com",
		"SPAMMER@EXAMPLE.COM":     "spammer@example.com",
		"@Example.com":            "@example.com",
		"a@b.co":                  "a@b.co",
	}
	for raw, stored := range valid {
		normalized, err := ValidatePattern(raw)
		require.NoError(t, err, "%q", raw)
		assert.Equal(t, stored, normalized, "%q", raw)
	}

	refused := []string{
		"",
		"no-at-sign",
		"two@at@signs.com",
		"@",
		"user@",
		"@do main.com",
		"wild*card@example.com",
		"*@example.com",
		"*@*.example.com",
		"prose@example.com not an entry",
	}
	for _, raw := range refused {
		_, err := ValidatePattern(raw)
		assert.ErrorIs(t, err, ErrPatternInvalid, "%q", raw)
	}
}

// TestMatchesPinsTheGrammarTheGateApplies pins the match: exact, the
// address's own domain, the subaddress carry-over — and the edges the
// grammar deliberately refuses, a near-miss domain and the dots that belong
// to the other feature.
func TestMatchesPinsTheGrammarTheGateApplies(t *testing.T) {
	patterns := []string{"john.doe@example.com", "@spam.example"}

	cases := []struct {
		address string
		blocked bool
	}{
		// The exact entry, in its own case.
		{"JOHN.DOE@EXAMPLE.COM", true},
		{"john.doe@example.com", true},
		// The carry-over: the same base, subaddressed.
		{"john.doe+newsletter@example.com", true},
		{"john.doe=tag@example.com", true},
		{"john.doe#tag@example.com", true},
		// A different address at the blocked address's domain is not the
		// blocked address — the entry is the address, not the domain.
		{"jane@example.com", false},
		// The domain entry: the same domain, any address.
		{"anyone@spam.example", true},
		{"ANYONE@SPAM.EXAMPLE", true},
		// A subdomain is a different domain — the wildcard grammar this
		// list does not carry would be the way to name it.
		{"anyone@mail.spam.example", false},
		// The near-miss the suffix match would read wrongly.
		{"anyone@notspam.example", false},
		{"anyone@example.com", false},
		// The dots are not separators: the base keeps them, so a folded
		// address is a different address until its own feature says so.
		{"john.doe+x@example.com", true},
		{"johndoe@example.com", false},
		// Malformed addresses match nothing.
		{"not-an-address", false},
		{"", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.blocked, Matches(tc.address, patterns), "%q", tc.address)
	}
}

// TestSubaddressBasePinsTheProviderSeparators pins the base: the generic
// separators everywhere, the hyphen only at Yahoo's own domains — exact
// domains, not a prefix — and nothing where no separator appears.
func TestSubaddressBasePinsTheProviderSeparators(t *testing.T) {
	cases := map[string]string{
		// Generic separators, cut at the first.
		"foo+tag@example.com": "foo@example.com",
		"foo=tag@x.com":       "foo@x.com",
		"foo#tag=x@x.com":     "foo@x.com",
		"Foo+Tag@Example.COM": "foo@example.com",
		// Yahoo: the hyphen cuts too, on the exact domains.
		"foo-tag@yahoo.com":   "foo@yahoo.com",
		"foo+bar@ymail.com":   "foo@ymail.com",
		"foo-bar@yahoo.co.uk": "foo@yahoo.co.uk",
		// A domain that begins with a `yahoo` label is not Yahoo.
		"foo-bar@yahoo.dev":          "foo-bar@yahoo.dev",
		"foo-bar@mail.yahoo.example": "foo-bar@mail.yahoo.example",
		// No separator, no change.
		"foobar@example.com": "foobar@example.com",
		// The dots are not separators in this match.
		"foo.bar+baz@gmail.com": "foo.bar@gmail.com",
		// Not an address: returned as it came.
		"not-an-address": "not-an-address",
	}
	for address, base := range cases {
		assert.Equal(t, base, SubaddressBase(address), "%q", address)
	}
}
