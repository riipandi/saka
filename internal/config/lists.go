package config

import "strings"

// listKeys are the config keys whose value is a list of names, and which a
// config file therefore writes as a JSON array.
//
// A directive is the reason this exists. `"transport": "env:LOG_TRANSPORT"` is
// one string by the time the file layer has resolved it, and a list written as
// one comma-separated string is the only form an environment variable can carry:
// `LOG_TRANSPORT=console,file`. Without this step koanf decodes that string into
// a one-element list holding "console,file", which names no sink.
//
// A test asserts this list is exactly the set of slice-valued fields on Config,
// so adding one fails until it is listed here.
var listKeys = []string{
	"log.transport",
	"oidc.cimd_url_allowlist",
	"server.cors.allowed_headers",
	"server.cors.allowed_methods",
	"server.cors.allowed_origins",
	"server.cors.exposed_headers",
	"server.trusted_proxy_headers",
}

// mapKeys are the config keys whose value is a map of its own, and which a
// config file therefore writes as a JSON object rather than as a section.
//
// The distinction matters at load time. A section is walked into dotted keys
// (otel.tracing.enable), which is how koanf merges one leaf without replacing
// its siblings. A map is one value holding several entries whose names the user
// chooses: walking otel.headers would produce otel.headers.authorization, a key
// that is not part of Config, and filterKnown would drop it — leaving the header
// silently unset rather than reported.
//
// A test asserts every key here resolves to a map-valued field on Config, so a
// rename cannot leave a key behind that nothing reads.
var mapKeys = []string{
	"otel.headers",
}

// normalizeMaps reads every map key of a layer as a map when it holds a string,
// so a directive can carry several entries in the one value an environment
// variable can hold: OTEL_HEADERS="authorization=Bearer x,x-tenant=acme".
//
// Only a named key is split, and only a string: a JSON object is already the
// shape the field wants, and a string under any other key is an ordinary value.
func normalizeMaps(keys map[string]any) {
	for _, key := range mapKeys {
		value, ok := keys[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		keys[key] = splitMap(text)
	}
}

// splitMap reads a comma-separated list of name=value pairs, the form the
// OpenTelemetry specification uses for its own headers variable.
//
// An entry with no "=" is dropped rather than kept as a name with no value: a
// header the collector would reject is worse than one that is plainly absent,
// and the exporter reports a malformed header far from the key that caused it.
func splitMap(text string) map[string]string {
	out := make(map[string]string)
	for entry := range strings.SplitSeq(text, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(entry), "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		out[name] = strings.TrimSpace(value)
	}
	return out
}

// normalizeLists reads every list key of a layer as a list of names when it
// holds a string, so a directive can name several with commas.
//
// Only a named key is split, and only a string: a JSON array is already the
// shape the field wants, and a string under any other key is an ordinary value.
func normalizeLists(keys map[string]any) {
	for _, key := range listKeys {
		value, ok := keys[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		keys[key] = splitList(text)
	}
}

// splitList reads a comma-separated list. Space around an entry is dropped, so
// `console, file` and `console,file` mean the same thing, and an empty entry is
// dropped rather than kept as a name no transport matches.
func splitList(text string) []string {
	parts := strings.Split(text, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
