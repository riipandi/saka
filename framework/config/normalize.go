package config

import (
	"slices"
	"strings"
	"time"
)

// FlattenNested walks a nested document into flat dotted keys, the form every
// layer is merged in.
//
// A key listed in mapKeys is kept as one value holding its entries: walking it
// would produce keys of the form name.entry, which the schema does not know
// and FilterKnown would drop — leaving the value silently unset rather than
// reported.
func FlattenNested(nested map[string]any, mapKeys []string) map[string]any {
	out := make(map[string]any, len(nested))
	walkNested(nested, nil, mapKeys, out)
	return out
}

func walkNested(nested map[string]any, prefix []string, mapKeys []string, out map[string]any) {
	for name, value := range nested {
		path := append(prefix, name)
		if child, ok := value.(map[string]any); ok && !slices.Contains(mapKeys, strings.Join(path, Delim)) {
			walkNested(child, path, mapKeys, out)
			continue
		}
		out[strings.Join(path, Delim)] = value
	}
}

// FilterKnown drops every entry whose key the schema does not know. A variable,
// an env-file line, or a flag that names no config key is ignored rather than
// allowed to create a stray entry or replace a whole section.
func FilterKnown(layer map[string]any, known map[string]any) map[string]any {
	out := make(map[string]any, len(layer))
	for key, value := range layer {
		if _, ok := known[key]; ok {
			out[key] = value
		}
	}
	return out
}

// NormalizeDurations reads every duration key of a layer as seconds when it
// holds a number, so a config file can say 900 rather than "15m".
//
// Only a named key is converted, and only a number: a duration string such as
// "15m" is left for the decoder to parse, and a number under any other key stays
// the type error it is.
func NormalizeDurations(keys map[string]any, durationKeys []string) {
	for _, key := range durationKeys {
		value, ok := keys[key]
		if !ok {
			continue
		}
		seconds, ok := asSeconds(value)
		if !ok {
			continue
		}
		keys[key] = time.Duration(seconds * float64(time.Second))
	}
}

// asSeconds reads a number of seconds from the forms a source may hand over: a
// JSON number arrives as a float64, and a caller building Options.Flags may pass
// any Go numeric type. A string is not a number here, even though the decoder
// would read "900" as one: a quoted value is a duration string, and the unit it
// carries is the one it means.
func asSeconds(value any) (float64, bool) {
	var seconds float64
	switch number := value.(type) {
	case float64:
		seconds = number
	case float32:
		seconds = float64(number)
	case int:
		seconds = float64(number)
	case int32:
		seconds = float64(number)
	case int64:
		seconds = float64(number)
	case uint:
		seconds = float64(number)
	case uint32:
		seconds = float64(number)
	case uint64:
		seconds = float64(number)
	default:
		return 0, false
	}

	// Reject NaN, an infinity, and a value that would overflow the
	// multiplication, so a nonsense number cannot wrap into a duration that
	// looks plausible.
	if seconds != seconds || seconds > maxDurationSeconds || seconds < -maxDurationSeconds {
		return 0, false
	}
	return seconds, true
}

// maxDurationSeconds is the largest number of seconds a time.Duration can hold,
// about 292 years.
const maxDurationSeconds = float64(1<<63-1) / float64(time.Second)

// NormalizeMaps reads every map key of a layer as a map when it holds a string,
// so a directive can carry several entries in the one value an environment
// variable can hold: OTEL_HEADERS="authorization=Bearer x,x-tenant=acme".
//
// Only a named key is split, and only a string: a JSON object is already the
// shape the field wants, and a string under any other key is an ordinary value.
func NormalizeMaps(keys map[string]any, mapKeys []string) {
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

// NormalizeLists reads every list key of a layer as a list of names when it
// holds a string, so a directive can name several with commas.
//
// Only a named key is split, and only a string: a JSON array is already the
// shape the field wants, and a string under any other key is an ordinary value.
func NormalizeLists(keys map[string]any, listKeys []string) {
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
