package config

import (
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The predicates below are the shape checks Validate applies to one value. They
// each answer a question about a single field, with no knowledge of the section
// it came from, which is why they are separate from the checks that name the
// key a problem belongs to.

// isPostgresDSN reports whether value parses as a Postgres connection string.
// The parse is pgx's, so a DSN that passes here connects the same way the
// datastore will read it.
func isPostgresDSN(value string) bool {
	_, err := pgx.ParseConfig(value)
	return err == nil
}

// maxFetcherRetries is the most extra attempts one call may make. Past this
// a typo in the file becomes a retry storm against someone else's service.
const maxFetcherRetries = 5

// maxFetcherBody is the largest response body a deployment may keep. Above
// this a typed number in the file is a typo that would buffer without a
// useful bound.
const maxFetcherBody = 32 << 20

// isKVURL reports whether value is a key-value connection string.
//
// The rules mirror the URL grammar the Valkey client accepts
// (valkey.ParseURL in github.com/valkey-io/valkey-go), because validation that is
// stricter than the client rejects a URL that would have connected:
//
//   - redis, rediss (TLS), and unix (a unix socket) are the accepted schemes
//   - a missing host is not an error: the client defaults it to localhost:6379
//   - the path is the database index, and is empty or one integer segment
//   - a unix socket carries its path instead of a host
//
// A query parameter is left to the client, which rejects an unexpected one when
// it connects. Mirroring that list here would be a second copy of it to keep in
// step, and it does not decide whether the URL names a server.
func isKVURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	switch parsed.Scheme {
	case "redis", "rediss":
		return isKVDBPath(parsed.Path)
	case "unix":
		return parsed.Path != ""
	default:
		return false
	}
}

// isKVDBPath reports whether a key-value URL path is the database index: empty,
// or a single integer segment. The client rejects anything else.
func isKVDBPath(path string) bool {
	segments := strings.FieldsFunc(path, func(r rune) bool { return r == '/' })
	switch len(segments) {
	case 0:
		return true
	case 1:
		_, err := strconv.Atoi(segments[0])
		return err == nil
	default:
		return false
	}
}

// isHTTPURL reports whether value is an absolute http or https URL.
func isHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}

// isHeaderValue reports whether value can be one HTTP header field value:
// non-empty, no control characters, and short enough to be a product token.
func isHeaderValue(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// corsWildcard is the origin entry that opens the policy to every origin.
const corsWildcard = "*"

// isOrigin reports whether value is what a browser sends in the Origin header:
// scheme://host, with no path, query, or trailing slash. A wrong form would
// never match a real origin, so the policy would be silently closed.
func isOrigin(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != "" && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

// isToken reports whether value is a valid HTTP token: the form a method and a
// header name must take.
func isToken(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		switch {
		case 'a' <= char && char <= 'z', 'A' <= char && char <= 'Z', '0' <= char && char <= '9':
		case strings.ContainsRune(tokenExtraChars, char):
		default:
			return false
		}
	}
	return true
}

// tokenExtraChars are the punctuation an HTTP token may carry besides letters
// and digits, as RFC 9110 defines them.
const tokenExtraChars = "!#$%&'*+-.^_`|~"

// isOTELPath reports whether value is a usable OTLP route: empty, or a URL path
// that starts with a slash and carries no scheme or host.
//
// It is what a signal's collector route is held to. A full URL there would be a
// second way to name a collector, which is the thing one shared otel.endpoint
// exists to prevent, so it is refused rather than merged. Empty is accepted
// because it means the protocol's own route.
func isOTELPath(value string) bool {
	if value == "" {
		return true
	}
	if !strings.HasPrefix(value, "/") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	return parsed.Scheme == "" && parsed.Host == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

// isPath reports whether value is a URL path: it starts with a slash and
// carries no scheme or host. It is what the Prometheus endpoint is held to,
// which must name a path rather than be empty.
func isPath(value string) bool {
	return value != "" && isOTELPath(value)
}

// isEmail reports whether value is an email address. net/mail accepts a bare
// local part and a display name, so the address form is what is checked here:
// the config holds the address alone, and a display name belongs in FromName.
func isEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	if err != nil {
		return false
	}
	return address.Address == value && strings.Contains(value, "@")
}
