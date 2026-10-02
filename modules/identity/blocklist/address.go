package blocklist

import (
	"regexp"
	"strings"
)

// genericSeparators are the subaddress separators every provider reads.
const genericSeparators = "+=#"

// yahooDomains maps the exact domains whose subaddress separators include
// the hyphen. A domain that merely begins with a `yahoo` label — `yahoo.dev`
// or a tenant subdomain on a hosting provider — is not Yahoo and reads the
// generic separators.
var yahooDomains = map[string]bool{
	"yahoo.com":      true,
	"ymail.com":      true,
	"rocketmail.com": true,
	"y7mail.com":     true,
	"yahoo.co.uk":    true,
	"yahoo.fr":       true,
	"yahoo.co.jp":    true,
	"yahoo.com.br":   true,
}

// gmailDomains maps the domains whose local part folds its dots. Folding is
// the block-email-subaddresses feature's match — the blocklist's carry-over
// keeps the dots, so the two answers live in different bases.
var gmailDomains = map[string]bool{
	"gmail.com":      true,
	"googlemail.com": true,
}

// patternShape is the grammar an entry parses to: one email address, or one
// `@domain` entry. It mirrors the column's check constraint — the database
// is the second reader of the same rule.
var patternShape = regexp.MustCompile(`^([a-z0-9._%+=-]+@[a-z0-9.-]+|@[a-z0-9.-]+)$`)

// domainShape is the domain half of an entry: dot-separated labels of
// alphanumerics and inner hyphens. A single-label domain (`@localhost`) is
// an address no public sign-up carries, but the grammar admits it — the
// blocklist's callers name real mail domains, and the shape is theirs to
// name.
var domainShape = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// ValidatePattern normalizes one entry and reports whether it parses. The
// stored form is lowercased and trimmed; anything the shape refuses is the
// caller's invalid-argument failure, not a silent prefix.
func ValidatePattern(pattern string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(pattern))
	if !patternShape.MatchString(normalized) {
		return "", ErrPatternInvalid
	}
	domain := normalized
	if at := strings.Index(normalized, "@"); at > 0 {
		domain = normalized[at+1:]
	} else if at == 0 {
		domain = normalized[1:]
	}
	if !domainShape.MatchString(domain) {
		return "", ErrPatternInvalid
	}
	return normalized, nil
}

// SubaddressBase returns the address with its subaddress removed: the local
// part cut at the first separator the address's provider reads, lowercased.
// The blocklist's carry-over matches a blocked exact address against the
// subaddressed variants of it; the separator set is the provider's, so a
// Yahoo local part also cuts at the hyphen. The dots stay — folding them is
// the block-email-subaddresses feature's match, not this one's, and an
// address whose separators never appear comes back as itself.
func SubaddressBase(address string) string {
	local, domain, ok := splitAddress(address)
	if !ok {
		return address
	}
	separators := genericSeparators
	if yahooDomains[domain] {
		separators += "-"
	}
	if cut := strings.IndexAny(local, separators); cut >= 0 {
		local = local[:cut]
	}
	return local + "@" + domain
}

// CollisionBase returns the form the block-email-subaddresses feature
// compares: the address with its subaddress removed — the same cut
// SubaddressBase makes — and, at a Gmail domain, the local part's dots
// folded away. Two addresses with one collision base are one mailbox.
func CollisionBase(address string) string {
	local, domain, ok := splitAddress(address)
	if !ok {
		return address
	}
	separators := genericSeparators
	if yahooDomains[domain] {
		separators += "-"
	}
	if cut := strings.IndexAny(local, separators); cut >= 0 {
		local = local[:cut]
	}
	if gmailDomains[domain] {
		local = strings.ReplaceAll(local, ".", "")
	}
	return local + "@" + domain
}

// Matches answers whether the address is refused for the patterns: an exact
// entry matches the address or its subaddress base — the carry-over — and
// an `@domain` entry matches the address's domain exactly, so a subdomain is
// a different domain the way it is for the wildcard grammar this list does
// not carry.
func Matches(address string, patterns []string) bool {
	address = strings.ToLower(strings.TrimSpace(address))
	base := SubaddressBase(address)
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "@") {
			if domainOf(address) == pattern[1:] {
				return true
			}
			continue
		}
		if address == pattern || base == pattern {
			return true
		}
	}
	return false
}

// splitAddress splits the address at its last `@` — the separator a
// quoted-local part may contain, though the grammar an entry accepts never
// carries one.
func splitAddress(address string) (local, domain string, ok bool) {
	local, domain, ok = strings.Cut(address, "@")
	if !ok || local == "" || domain == "" {
		return "", "", false
	}
	return strings.ToLower(local), strings.ToLower(domain), true
}

// domainOf reads the address's domain half, lowercased.
func domainOf(address string) string {
	_, domain, ok := splitAddress(address)
	if !ok {
		return ""
	}
	return domain
}
