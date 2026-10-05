package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"encoding/json/v2"
)

// DefaultConfigFile is the config file Resolve reads when no source names one.
// The file is the single source of truth for the configuration, so a missing
// default file is an error: a run without it would silently fall back to the
// built-in defaults, which is not a configuration anyone chose.
const DefaultConfigFile = "app.config.json"

// FileEnv is the environment variable naming the JSON config file. It is
// overridden by an explicit --config-file flag.
const FileEnv = "CONFIG_FILE"

// ErrNoConfigFile reports a config file that does not exist.
var ErrNoConfigFile = errors.New("config: config file not found")

// ErrUnresolvedVar reports an interpolation directive naming a variable that is
// not set. An empty value is a valid value; an absent variable is not, because
// it would silently blank a key.
var ErrUnresolvedVar = errors.New("config: unresolved variable")

// ConfigPath returns the config file Resolve reads: the one named by
// Options.ConfigFile, then FileEnv, then DefaultConfigFile in the working
// directory.
func ConfigPath(opts Options, environ []string) string {
	if opts.ConfigFile != "" {
		return opts.ConfigFile
	}
	if name := lookupValue(environ, FileEnv); name != "" {
		return name
	}
	return DefaultConfigFile
}

// configFileLayer reads the JSON config file and returns its keys, flattened and
// with interpolation resolved.
//
// The environment is not a layer: it is the table the file's env: and ${...}
// directives resolve from, so a value can name a secret that never appears in
// the file while the file remains the only thing that decides which keys exist.
//
// A directive naming a variable that is not set is not a load failure. It is
// reported as unresolved and the key is left out of the layer, so it keeps its
// default and only the caller that reads it is affected. The schema's
// validation reports every unresolved key.
func configFileLayer(opts Options, environ []string, schema Schema) (map[string]any, map[string]string, error) {
	path := ConfigPath(opts, environ)

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("%w: %s", ErrNoConfigFile, path)
		}
		return nil, nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	flat := FlattenNested(doc, schema.Maps)
	unresolved := make(map[string]string)
	for key, value := range flat {
		text, ok := value.(string)
		if !ok {
			continue
		}
		resolved, missing, err := interpolate(text, environ)
		if err != nil {
			return nil, nil, fmt.Errorf("config: %s: key %s: %w", path, key, err)
		}
		if missing != "" {
			// Drop the key rather than keep the directive: the raw "env:NAME"
			// text is not a value, and a key left at its default is the honest
			// state of a value nobody supplied.
			unresolved[key] = missing
			delete(flat, key)
			continue
		}
		flat[key] = resolved
	}
	return FilterKnown(flat, schema.Defaults), unresolved, nil
}

// interpolate expands the two directives a config file may use: env:NAME, the
// whole value, and ${NAME}, inline. Both resolve from the environment, so a
// value can reference a secret that never appears in the file.
//
// The second result names the variable that was not set, empty when the value
// resolved. A bare $NAME is left alone: it is a common character in a password
// or a URL, and expanding it would corrupt a value the user meant literally.
func interpolate(value string, environ []string) (string, string, error) {
	if name, ok := strings.CutPrefix(value, "env:"); ok {
		if name == "" {
			return "", "", fmt.Errorf("%w: env: with no name", ErrUnresolvedVar)
		}
		resolved, found := lookup(environ, name)
		if !found {
			return "", name, nil
		}
		return resolved, "", nil
	}

	if !strings.Contains(value, "${") {
		return value, "", nil
	}
	return expandInline(value, environ)
}

// expandInline replaces every ${NAME} in value. An unterminated directive is
// literal text, not an error: a value such as "cost is ${5" is a value, not a
// reference.
func expandInline(value string, environ []string) (string, string, error) {
	var out strings.Builder
	rest := value
	for {
		start := strings.Index(rest, "${")
		if start < 0 {
			out.WriteString(rest)
			return out.String(), "", nil
		}
		out.WriteString(rest[:start])

		end := strings.Index(rest[start:], "}")
		if end < 0 {
			out.WriteString(rest[start:])
			return out.String(), "", nil
		}

		name := rest[start+2 : start+end]
		resolved, found := lookup(environ, name)
		if !found {
			return "", name, nil
		}
		out.WriteString(resolved)
		rest = rest[start+end+1:]
	}
}
