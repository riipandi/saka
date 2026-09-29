package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// A caller validates a Config it did not write, and renders one safely when it
// prints it. The rendering half lives here: every path that turns a Config into
// text goes through one of these, so there is a single place that decides what
// a secret looks like on the way out.

// redacted is the placeholder a Redacted Config prints instead of a secret.
const redacted = "[redacted]"

// Masking shows a little of a secret so a value can be traced back to its
// source. It is for a report the operator runs against their own configuration,
// never for a log line: see Masked.
const (
	// maskKeep is how many characters are kept at each end of a masked value.
	maskKeep = 4
	// maskSeparator stands in for the hidden middle. It is what makes a masked
	// value recognisable as masked rather than as a short secret.
	maskSeparator = "****"
	// maskMinLength is the shortest value that is masked at all. A shorter one
	// is redacted in full: keeping eight characters of a twelve-character
	// secret would leave most of it readable. Every secret this configuration
	// actually holds is far longer, so the floor only catches a short password,
	// where hiding it completely is the safe answer.
	maskMinLength = 16
)

// mask renders a secret for a report: the first and last few characters with
// the middle replaced, so two keys can be told apart by eye.
//
// An empty value stays empty, so an unset secret is told apart from a set one;
// a value below maskMinLength is redacted in full.
func mask(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) < maskMinLength {
		return redacted
	}
	return secret[:maskKeep] + maskSeparator + secret[len(secret)-maskKeep:]
}

// secretRenderer renders one secret for display.
type secretRenderer func(string) string

// redactHeaders renders every header value through the secret path.
//
// A header is a secret as a whole rather than by name: an authorization token is
// why the key exists, and a map is one config value, so there is no key to list
// per entry. The names are kept, because a name says which credential is missing
// without revealing it, and they are read by a person rather than a matcher.
func redactHeaders(headers map[string]string, render secretRenderer) map[string]string {
	if headers == nil {
		return nil
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		out[name] = render(value)
	}
	return out
}

// withSecrets returns a copy with every secret passed through render.
//
// A connection string is always reduced to host:port/database rather than
// rendered: it is a composite value, so revealing part of the string says
// nothing, while the host is the part a reader needs.
func (c Config) withSecrets(render secretRenderer) Config {
	out := c
	out.App.SecretKey = render(c.App.SecretKey)
	out.Auth.SecretKey = render(c.Auth.SecretKey)
	out.Database.URL = RedactDSN(c.Database.URL)
	out.KVStore.URL = RedactKVURL(c.KVStore.URL)
	out.Mailer.SMTPPassword = render(c.Mailer.SMTPPassword)
	out.Storage.S3.AccessKey = render(c.Storage.S3.AccessKey)
	out.Storage.S3.SecretKey = render(c.Storage.S3.SecretKey)
	out.OTEL.Headers = redactHeaders(c.OTEL.Headers, render)
	out.origin = nil
	out.unresolved = nil
	return out
}

// Redacted returns a copy with every secret replaced by a placeholder, safe to
// print or log.
//
// This is the fail-safe. A Config rendered by %v, or a report that has no
// business showing a secret at all, gets this one: it shows nothing of the
// value, so there is no partial leak to reason about.
func (c Config) Redacted() Config {
	return c.withSecrets(func(string) string { return redacted })
}

// Masked returns a copy with every secret partially hidden: enough of each value
// to recognise it, never enough to use it. It is what `config:print` renders.
//
// It is deliberately not what a log line gets. A log is read by everyone with
// access to the logs, so a partial secret there is a secret that leaked slowly;
// a report is read by the operator, who could read the value anyway. Keeping the
// two apart is what lets the fail-safe stay absolute.
func (c Config) Masked() Config {
	return c.withSecrets(mask)
}

// RedactDSN reduces a Postgres connection string to host:port/database, the form
// the CLI prints for a database target. An unparsable string is replaced
// wholesale, because it may still carry a password.
func RedactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		return redacted
	}
	return fmt.Sprintf("%s:%d/%s", parsed.Host, parsed.Port, parsed.Database)
}

// RedactKVURL reduces a key-value connection string to host:port/database, the
// same rendering RedactDSN gives a Postgres one.
//
// It does not reuse RedactDSN: that parse is pgx's, and a redis:// URL is not a
// Postgres connection string, so pgx rejects it and the whole value would be
// replaced by the placeholder. The point of the rendering is to name the server
// a command talks to, which a placeholder cannot do.
//
// A missing host is not a failure here either: the client would default it to
// localhost:6379, so the target says the same rather than hiding the URL.
func RedactKVURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return redacted
	}
	if parsed.Scheme == "unix" {
		return parsed.Path
	}
	target := parsed.Host
	if target == "" {
		target = "localhost:6379"
	}
	if db := strings.TrimPrefix(parsed.Path, "/"); db != "" {
		target += "/" + db
	}
	return target
}
