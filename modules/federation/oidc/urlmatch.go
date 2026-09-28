package oidc

import (
	"errors"
	"log/slog"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/dunglas/go-urlpattern"
)

// The callback-URL matcher, ported from upstream Pocket ID's
// `utils/callback_url_util.go` (backend/internal/utils): the redirect URI
// rules a client's callbacks are judged by, and the pattern grammar the
// CIMD allowlist is written in. A pattern supports single `*` wildcards in
// the base and path, `**` globstars in the path, and wildcard query
// values; an exact string or a bare `*` matches as itself.
//
// The endpoint URI MUST NOT include a fragment component (RFC 6749
// §3.1.2), so both sides lose theirs before the comparison.

// MatchesAnyURLPattern reports whether the input matches any pattern in
// the list, using the same wildcard rules as the callback URLs. An empty
// list never matches — the CIMD refusal is silent by design.
func MatchesAnyURLPattern(patterns []string, input string) bool {
	for _, pattern := range patterns {
		if matches, err := matchCallbackURL(pattern, input); err == nil && matches {
			return true
		}
	}
	return false
}

// ValidateCallbackURLPattern checks the pattern is written in the grammar
// this matcher reads: a scheme (not `javascript`/`data`), optional
// wildcards, no fragment.
func ValidateCallbackURLPattern(pattern string) error {
	if pattern == "*" {
		return nil
	}

	pattern, _, _ = strings.Cut(pattern, "#")
	parsed, err := url.Parse(callbackURLPatternForURLParse(pattern))
	if err != nil {
		return err
	}
	if parsed.Scheme == "" {
		return errors.New("callback URL pattern must include a scheme")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "javascript", "data":
		return errors.New("callback URL pattern scheme is not allowed")
	}

	_, err = urlpattern.New(normalizeToURLPatternStandard(pattern), "", nil)
	return err
}

// MatchCallbackURLFromList answers the first registered URL that matches
// the input, the way the redirect and post-logout comparisons run. An
// empty match means none of the registered URLs is the one presented.
func MatchCallbackURLFromList(urls []string, input string) (string, error) {
	loopbackWithoutPort := loopbackURLWithWildcardPort(input)

	for _, pattern := range urls {
		matches, err := matchCallbackURL(pattern, input)
		if err != nil {
			return "", err
		}
		if matches {
			return input, nil
		}

		// RFC 8252 §7.3: a loopback redirect URI may carry any port — the
		// OS hands the app an ephemeral one at authorization time — so the
		// comparison tries the port-less loopback form beside the input.
		if loopbackWithoutPort != "" {
			matches, err = matchCallbackURL(pattern, loopbackWithoutPort)
			if err != nil {
				return "", err
			}
			if matches {
				return input, nil
			}
		}
	}
	return "", nil
}

// matchCallbackURL compares one pattern against one input: the query
// parameters must match exactly in count (values through path.Match), the
// rest through the URL pattern.
func matchCallbackURL(pattern, input string) (bool, error) {
	if pattern == input || pattern == "*" {
		return true, nil
	}

	pattern, _, _ = strings.Cut(pattern, "#")
	input, _, _ = strings.Cut(input, "#")

	pattern, patternQuery, err := extractQueryParams(pattern)
	if err != nil {
		return false, err
	}
	input, inputQuery, err := extractQueryParams(input)
	if err != nil {
		return false, err
	}

	if !validateQueryParams(patternQuery, inputQuery) {
		return false, nil
	}

	compiled, err := urlpattern.New(normalizeToURLPatternStandard(pattern), "", nil)
	if err != nil {
		slog.Warn("invalid callback URL pattern, skipping", "error", err)
		return false, nil
	}
	return compiled.Test(input, ""), nil
}

// callbackURLPatternForURLParse prepares a pattern for url.Parse: the
// `*://` scheme becomes https for parsing, and a wildcard port becomes 443
// — a port url.Parse reads — while the wildcard survives in the original.
func callbackURLPatternForURLParse(pattern string) string {
	if after, ok := strings.CutPrefix(pattern, "*://"); ok {
		pattern = "https://" + after
	}

	scheme, rest, ok := strings.Cut(pattern, "://")
	if !ok {
		return pattern
	}

	authority := rest
	suffix := ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority = rest[:i]
		suffix = rest[i:]
	}

	userinfo := ""
	hostport := authority
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		userinfo = authority[:i+1]
		hostport = authority[i+1:]
	}

	if strings.HasPrefix(hostport, "[") {
		end := strings.Index(hostport, "]")
		if end == -1 {
			return pattern
		}
		if len(hostport) > end+1 && hostport[end+1] == ':' && strings.Contains(hostport[end+2:], "*") {
			hostport = hostport[:end+2] + "443"
		}
	} else if i := strings.LastIndex(hostport, ":"); i >= 0 && strings.Contains(hostport[i+1:], "*") {
		hostport = hostport[:i+1] + "443"
	}

	return scheme + "://" + userinfo + hostport + suffix
}

// normalizeToURLPatternStandard converts the pattern grammar (`*`, `**`)
// into the grammar go-urlpattern reads: a single `*` becomes a one-segment
// `:p` wildcard, a `**` globstar becomes the multi-segment `*`, and every
// literal colon — IPv6 addresses, a path's own colon — is escaped.
func normalizeToURLPatternStandard(pattern string) string {
	patternBase, patternPath := extractPath(pattern)

	var result strings.Builder
	result.Grow(len(pattern) + 5)

	writeNormalizedBase(&result, patternBase)
	writeNormalizedPath(&result, patternPath)

	return result.String()
}

// writeNormalizedBase escapes the colons in the scheme and authority that
// urlpattern would otherwise read as wildcards.
func writeNormalizedBase(result *strings.Builder, patternBase string) {
	const (
		stepScheme = iota
		stepHost
		stepIPv6
		stepAfterHost
	)

	var step int
	for i := 0; i < len(patternBase); i++ {
		switch step {
		case stepScheme:
			if i > 3 && patternBase[i] == '/' && patternBase[i-1] == '/' && patternBase[i-2] == ':' {
				step = stepHost
			}
		case stepHost:
			switch patternBase[i] {
			case '/', ']':
				step = stepAfterHost
			case '[':
				step = stepIPv6
			case ':':
				// A colon introducing a port is followed by a digit, so it
				// stays structural; everything else is a literal.
				if !isPortSeparator(patternBase, i) {
					result.WriteByte('\\')
				}
			}
		case stepIPv6:
			switch patternBase[i] {
			case ':':
				result.WriteByte('\\')
			case '/', ']', '[':
				step = stepAfterHost
			}
		}

		result.WriteByte(patternBase[i])
	}
}

// writeNormalizedPath converts `*` and `**` into the wildcards urlpattern
// understands, leaving every other character — a literal colon included —
// escaped or literal as the grammar needs.
func writeNormalizedPath(result *strings.Builder, patternPath string) {
	for i := 0; i < len(patternPath); i++ {
		switch patternPath[i] {
		case '*':
			if i+1 < len(patternPath) && patternPath[i+1] == '*' {
				result.WriteString("*")
				i++
			} else {
				result.WriteString(":p")
				result.WriteString(strconv.Itoa(i))
			}
		case ':':
			result.WriteByte('\\')
			result.WriteByte(patternPath[i])
		default:
			result.WriteByte(patternPath[i])
		}
	}
}

// isPortSeparator reports whether the colon at index i separates the host
// from a port.
func isPortSeparator(s string, i int) bool {
	return i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9'
}

// extractPath splits the base (scheme + authority) from the path.
func extractPath(rawurl string) (base, urlPath string) {
	pathStart := -1

	if i := strings.Index(rawurl, "://"); i >= 0 {
		if j := strings.IndexByte(rawurl[i+3:], '/'); j >= 0 {
			pathStart = i + 3 + j
		}
	} else {
		pathStart = strings.IndexByte(rawurl, '/')
	}

	if pathStart >= 0 {
		return rawurl[:pathStart], rawurl[pathStart:]
	}
	return rawurl, ""
}

// extractQueryParams splits the query from the rest of the URL.
func extractQueryParams(rawurl string) (string, url.Values, error) {
	if i := strings.IndexByte(rawurl, '?'); i >= 0 {
		query, err := url.ParseQuery(rawurl[i+1:])
		if err != nil {
			return "", nil, err
		}
		return rawurl[:i], query, nil
	}
	return rawurl, nil, nil
}

// validateQueryParams requires the pattern's query to be present in full:
// same keys, same counts, each value matched through the glob grammar.
func validateQueryParams(patternQuery, inputQuery url.Values) bool {
	if len(patternQuery) != len(inputQuery) {
		return false
	}

	for patternKey, patternValues := range patternQuery {
		inputValues, exists := inputQuery[patternKey]
		if !exists || len(patternValues) != len(inputValues) {
			return false
		}

		for i := range patternValues {
			if matched, err := path.Match(patternValues[i], inputValues[i]); err != nil || !matched {
				return false
			}
		}
	}
	return true
}

// loopbackURLWithWildcardPort renders the input's loopback form with the
// port dropped, the RFC 8252 §7.3 comparison's second face. A non-loopback
// input answers empty.
func loopbackURLWithWildcardPort(input string) string {
	u, _ := url.Parse(input)
	if u == nil || u.Scheme != "http" {
		return ""
	}

	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return ""
	}

	// IPv6 loopback hosts need the brackets back to serialize.
	if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	return u.String()
}
