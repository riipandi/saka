// Package config is the configuration engine: it resolves a configuration
// from one source of truth, a JSON config file, and the layers that may
// override it, and hands the result to a schema the caller owns.
//
// The engine knows no keys. The schema a caller supplies names the known
// keys (through their defaults), and the vocabularies the file's value forms
// need — which keys are durations, which are lists, which are maps. The
// engine merges layers, resolves the file's environment directives, records
// which source won each key, and decodes the result into whatever struct the
// caller declares.
//
// # Sources and precedence
//
// From lowest to highest:
//
//  1. the built-in defaults (Schema.Defaults)
//  2. the JSON config file (--config-file, CONFIG_FILE, or the default name)
//  3. the command-line flags
//
// Merge is last-wins per key, not per section: a source that sets one leaf
// leaves its siblings alone.
//
// # The config file is the source of truth
//
// The environment is not a layer. A variable reaches a config key only where
// the file references it, with env:NAME as a whole value or ${NAME} inline,
// so the file decides which keys exist and no key needs a fixed variable
// name. A variable nobody referenced cannot change a value, which is what
// keeps a stray export in a shell from altering a run.
//
// The file is required. A missing file is an error, not a fallback to the
// defaults: a run with no configuration is not a configuration anyone chose.
//
// # Composability
//
// Resolve is the whole engine: it takes its sources as values (the file
// path, the env-file pairs, the flag map, the environment slice) and its
// schema as a value, so a binary composes it without anything to inherit —
// no package state, no environment reads beyond the sources it is handed.
package config

import (
	"fmt"
	"maps"
	"os"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"
)

// Options describes the sources Resolve merges. Every field is optional: with
// no options the result is the schema's defaults.
type Options struct {
	// ConfigFile is the path to a JSON config file. When empty, FileEnv is
	// consulted, and DefaultConfigFile is tried last.
	ConfigFile string
	// EnvFile holds the key-value pairs of a dotenv file, already parsed. It
	// is not a layer: it is part of the table the config file's directives
	// resolve from, and it wins over the system environment for a name both
	// set.
	EnvFile map[string]string
	// Flags holds the command-line values to apply. It wins over every other
	// source. Keys are config paths, not flag names.
	Flags map[string]any
	// Environ is the system environment to read, in NAME=value form. It
	// defaults to os.Environ; a test passes a fixed slice.
	Environ []string
}

// Schema is the key vocabulary Resolve needs. The engine knows no keys; the
// schema names them.
type Schema struct {
	// Defaults is the built-in value of every known key, keyed by config
	// path. It is the lowest layer and the set of keys a source may set: a
	// key outside it is dropped, so a stray entry naming a section cannot
	// replace that section with a scalar and break the decode.
	Defaults map[string]any
	// Durations lists the keys whose file form is a plain number of seconds.
	Durations []string
	// Lists lists the keys whose file form may be one comma-separated string.
	Lists []string
	// Maps lists the keys whose file form is a JSON object, not a section.
	Maps []string
}

// Resolved is the outcome of Resolve: the merged configuration, ready to
// decode, with the bookkeeping that makes precedence observable.
type Resolved struct {
	k          *koanf.Koanf
	origin     map[string]string
	unresolved map[string]string
	table      []string
}

// Resolve resolves the configuration from every source, in the documented
// precedence order: the schema's defaults, then the JSON config file, then
// the command-line flags. The environment supplies values to the file's
// directives but is not itself a layer.
//
// Each layer is flattened before it is merged, so a nested object in the
// file and a flat flag override the same key.
//
// Resolve does not validate: a caller that needs one key must not be blocked
// by a key it never reads. Validation belongs to the schema's owner.
func Resolve(opts Options, schema Schema) (*Resolved, error) {
	environ := opts.Environ
	if environ == nil {
		environ = os.Environ()
	}
	table := interpolateEnv(environ, opts.EnvFile)

	r := &Resolved{
		origin:     make(map[string]string),
		unresolved: make(map[string]string),
		table:      table,
	}
	k := koanf.New(Delim)

	fileKeys, unresolved, err := configFileLayer(opts, table, schema)
	if err != nil {
		return nil, err
	}
	r.unresolved = unresolved

	layers := []struct {
		name string
		keys map[string]any
	}{
		{LayerDefault, schema.Defaults},
		{LayerConfigFile, fileKeys},
		{LayerFlag, FilterKnown(opts.Flags, schema.Defaults)},
	}
	for _, layer := range layers {
		// A duration key is read as seconds before the layer is merged, so
		// the unit is decided by the key rather than guessed by the decoder, a
		// list key written as one comma-separated string becomes the list the
		// field wants, and a map key written that way becomes the map it
		// wants.
		NormalizeDurations(layer.keys, schema.Durations)
		NormalizeLists(layer.keys, schema.Lists)
		NormalizeMaps(layer.keys, schema.Maps)
		if err := merge(k, r.origin, layer.name, layer.keys); err != nil {
			return nil, err
		}
	}
	r.k = k
	return r, nil
}

// merge applies one layer to the accumulated configuration and records it as
// the origin of the keys it set, replacing an earlier entry. The last layer
// to set a key is the one that won.
func merge(k *koanf.Koanf, origin map[string]string, layer string, keys map[string]any) error {
	if len(keys) == 0 {
		return nil
	}
	if err := k.Load(confmap.Provider(keys, Delim), nil); err != nil {
		return fmt.Errorf("config: load %s: %w", layer, err)
	}
	for key := range keys {
		origin[key] = layer
	}
	return nil
}

// Unmarshal decodes the resolved configuration into target, a struct whose
// koanf tags name the keys. A key no source set keeps the value the defaults
// layer carries, because the defaults were merged in first.
func (r *Resolved) Unmarshal(target any) error {
	return r.k.Unmarshal("", target)
}

// Unresolved returns the keys whose interpolation directive named a variable
// that is not set, mapped to the variable name. Such a key keeps its default
// and is reported by the schema's validation, so a caller that reads it gets
// an error naming the variable while one that does not is unaffected.
func (r *Resolved) Unresolved() map[string]string {
	out := make(map[string]string, len(r.unresolved))
	maps.Copy(out, r.unresolved)
	return out
}

// Origin reports which source last set key, or an empty string when no source
// set it. The names are LayerDefault, LayerConfigFile, and LayerFlag.
//
// It makes precedence observable: a key set by both the file and a flag
// reports LayerFlag.
func (r *Resolved) Origin(key string) string {
	return r.origin[key]
}

// Origins returns a copy of the source of every resolved key.
func (r *Resolved) Origins() map[string]string {
	out := make(map[string]string, len(r.origin))
	maps.Copy(out, r.origin)
	return out
}

// LookupEnv reads a variable from the environment table the file's directives
// resolved from: the env-file pairs first, the process environment second. It
// lets a schema carry a resolution rule of its own — a key that falls back to
// a variable no config key names — without the engine knowing the rule.
func (r *Resolved) LookupEnv(name string) string {
	return lookupValue(r.table, name)
}
