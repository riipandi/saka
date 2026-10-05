package authz

import "strings"

// Match reports whether a held permission satisfies a requirement.
//
// The requirement names one instance; the grant may name the wildcard. The
// segments compare exactly except the middle one, where `*` in the grant
// stands for any instance. A grant a catalog does not declare still
// matches — the catalog is the seed's input, not the matcher's gate —
// because a slug minted beside a feature must keep working after a catalog
// entry is renamed away under it.
func Match(requirement, grant string) bool {
	required := strings.Split(requirement, ":")
	held := strings.Split(grant, ":")
	if len(required) != 3 || len(held) != 3 {
		return false
	}
	for _, part := range append(required, held...) {
		if part == "" {
			return false
		}
	}
	if required[0] != held[0] || required[2] != held[2] {
		return false
	}
	return held[1] == Wildcard || held[1] == required[1]
}

// Grants reports whether the held set satisfies the requirement: one grant
// in the set matches it.
func Grants(held []string, requirement string) bool {
	for _, grant := range held {
		if Match(requirement, grant) {
			return true
		}
	}
	return false
}

// ValidSlug reports whether the string is a well-formed permission slug:
// three colon-separated segments over the slug alphabet, with a wildcard
// only in the middle position. It is the gate a role edit passes before a
// grant is written, so a malformed slug cannot enter the set a token later
// carries verbatim.
func ValidSlug(slug string) bool {
	parts := strings.Split(slug, ":")
	if len(parts) != 3 {
		return false
	}
	for i, part := range parts {
		if part == "" {
			return false
		}
		if part == Wildcard {
			// The wildcard is the middle segment's alone.
			if i != 1 {
				return false
			}
			continue
		}
		for _, r := range part {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			default:
				return false
			}
		}
	}
	return true
}
