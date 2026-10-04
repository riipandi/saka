package fetcher

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// slogAdapter is the logger Resty writes its own lines through. Debug is
// off, so these lines are Resty's warnings rather than a body dump. Each
// one is still scrubbed: a warning that quotes a URL would otherwise keep
// the query string.
type slogAdapter struct {
	log *slog.Logger
}

func (s slogAdapter) Errorf(format string, args ...any) {
	s.log.Error(redact(sprintf(format, args...)))
}

func (s slogAdapter) Warnf(format string, args ...any) {
	s.log.Warn(redact(sprintf(format, args...)))
}

func (s slogAdapter) Debugf(format string, args ...any) {
	s.log.Debug(redact(sprintf(format, args...)))
}

// sprintf formats a Resty log line. A line with no arguments is returned
// unchanged, so a literal percent sign is not read as a format verb.
func sprintf(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return strings.TrimRight(fmt.Sprintf(format, args...), "\n")
}

// Patterns a log line must not keep. Bearer and Basic run before the
// authorization header, because that header's value is itself a scheme
// plus a token and a single word would leave the token behind.
var (
	bearerOrBasic = regexp.MustCompile(`(?i)(bearer|basic)\s+\S+`)
	authHeader    = regexp.MustCompile(`(?i)((?:authorization|proxy-authorization)\s*[:=]\s*)\S+`)
	secretParam   = regexp.MustCompile(`(?i)((?:password|passwd|token|secret|api_key|apikey|access_key|access_token)=)[^&\s]+`)
	urlUserinfo   = regexp.MustCompile(`(https?://)[^/\s:@]+:[^/\s@]+@`)
	urlQuery      = regexp.MustCompile(`(https?://[^\s?]+)\?[^\s]+`)
)

// redact removes credentials and query strings from a log line. Labels stay,
// so a reader can see which field was removed.
func redact(line string) string {
	line = bearerOrBasic.ReplaceAllString(line, "${1} [redacted]")
	line = authHeader.ReplaceAllString(line, "${1}[redacted]")
	line = secretParam.ReplaceAllString(line, "${1}[redacted]")
	line = urlUserinfo.ReplaceAllString(line, "${1}")
	line = urlQuery.ReplaceAllString(line, "${1}")
	return line
}
