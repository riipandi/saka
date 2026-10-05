package config

import (
	"maps"
	"slices"
	"strings"
)

// Delim is the key separator used throughout the config layer. It is the same
// delimiter the JSON file uses for nesting, so a file key and a config key are
// the same string.
const Delim = "."

// Layer names, lowest precedence first. Resolve applies them in this order, so
// a later layer wins: the JSON config file replaces a built-in default, and a
// command-line flag replaces both.
//
// The environment is deliberately not a layer. A variable reaches a config key
// only where the config file references it, with env: or ${...}, so the file
// stays the single source of truth: a variable nobody referenced cannot change
// a value, and no key needs a fixed variable name.
const (
	LayerDefault    = "default"
	LayerConfigFile = "config-file"
	LayerFlag       = "flag"
)

// EnvName returns the conventional environment variable name for a config key:
// database.url becomes DATABASE_URL.
//
// It is a naming convention, not a mapping the loader applies: nothing reaches a
// config key by this name alone. It exists so the variable names a generated
// config file writes are the ones key generation writes and .env.example lists.
func EnvName(key string) string {
	return strings.ToUpper(strings.ReplaceAll(key, Delim, "_"))
}

// interpolateEnv builds the table the config file's directives resolve from. The
// values of --env-file come first, because lookup returns the first match and a
// user who named an env file meant it to be the one that answers.
func interpolateEnv(environ []string, values map[string]string) []string {
	table := make([]string, 0, len(environ)+len(values))
	for _, name := range slices.Sorted(maps.Keys(values)) {
		table = append(table, name+"="+values[name])
	}
	return append(table, environ...)
}

// lookup reads a variable from an environment slice.
func lookup(environ []string, name string) (string, bool) {
	prefix := name + "="
	for _, entry := range environ {
		if value, ok := strings.CutPrefix(entry, prefix); ok {
			return value, true
		}
	}
	return "", false
}

// lookupValue reads a variable from an environment slice, returning "" when it
// is absent.
func lookupValue(environ []string, name string) string {
	value, _ := lookup(environ, name)
	return value
}
