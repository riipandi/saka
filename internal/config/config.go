// Package config resolves the application configuration from one source of
// truth, a JSON config file, and the layers that may override it.
//
// # Sources and precedence
//
// From lowest to highest:
//
//  1. the built-in defaults (Default)
//  2. the JSON config file (--config-file, CONFIG_FILE, or app.config.json)
//  3. the command-line flags
//
// Merge is last-wins per key, not per section: a source that sets one leaf
// leaves its siblings alone.
//
// # The config file is the source of truth
//
// The environment is not a layer. A variable reaches a config key only where the
// file references it, with env:NAME as a whole value or ${NAME} inline, so the
// file decides which keys exist and no key needs a fixed variable name. A
// variable nobody referenced cannot change a value, which is what keeps a stray
// export in a shell from altering a run.
//
// The file is required. A missing file is an error, not a fallback to the
// defaults: a run with no configuration is not a configuration anyone chose.
// config:generate writes the file a fresh checkout starts from.
//
// # Key names
//
// A key is a dotted path, such as database.max_conns, which is also the variable
// name the generated file and .env.example use for it (see EnvName). That name
// is a convention for what the file writes, not a mapping the loader applies.
//
// A key that holds a length of time is written as a plain number of seconds, so
// the file says 900 rather than "15m0s". A duration string is still accepted; see
// durationKeys.
//
// # The struct is the schema
//
// Config and its sections define every key, its default (Default), and its rule
// (Validate). Adding a key means adding a field with a koanf and a json tag, a
// default, and a rule; the key then works in every source at once.
//
// # Conflict resolution is observable
//
// A resolved Config records which source last set each key, readable through
// Origin. That is how a caller tells a file value from a flag value without
// re-deriving the precedence.
package config

import (
	"fmt"
	"maps"
	"os"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"
)

// Options describes the sources Load merges. Every field is optional: with no
// options the result is Default().
type Options struct {
	// ConfigFile is the path to a JSON config file. When empty, FileEnv is
	// consulted, and DefaultConfigFile is tried last.
	ConfigFile string
	// EnvFile holds the key-value pairs of a dotenv file, already parsed. It is
	// not a layer: it is part of the table the config file's directives resolve
	// from, and it wins over the system environment for a name both set.
	EnvFile map[string]string
	// Flags holds the command-line values to apply. It wins over every other
	// source. Keys are config paths, not flag names.
	Flags map[string]any
	// Environ is the system environment to read, in NAME=value form. It
	// defaults to os.Environ; a test passes a fixed slice.
	Environ []string
}

// Load resolves the configuration from every source, in the documented
// precedence order: built-in defaults, then the JSON config file, then the
// command-line flags. The environment supplies values to the file's directives
// but is not itself a layer.
//
// Each layer is flattened before it is merged, so a nested object in the file
// and a flat flag override the same key.
//
// Only known keys are accepted. A key that is not part of Config is dropped, so
// a stray entry naming a section cannot replace that section with a scalar and
// break the decode.
//
// Load does not validate: a command that needs one key, such as a migration that
// needs only database.url, must not be blocked by a key it never reads. Call
// Validate where the whole configuration is required.
func Load(opts Options) (Config, error) {
	environ := opts.Environ
	if environ == nil {
		environ = os.Environ()
	}
	table := interpolateEnv(environ, opts.EnvFile)

	cfg := Default()
	cfg.origin = make(map[string]string)
	cfg.unresolved = make(map[string]string)
	k := koanf.New(Delim)

	fileKeys, unresolved, err := configFileLayer(opts, table)
	if err != nil {
		return Config{}, err
	}
	cfg.unresolved = unresolved

	layers := []struct {
		name string
		keys map[string]any
	}{
		{LayerDefault, DefaultsMap()},
		{LayerConfigFile, fileKeys},
		{LayerFlag, filterKnown(opts.Flags)},
	}
	for _, layer := range layers {
		// A duration key is read as seconds before the layer is merged, so the
		// unit is decided by the key rather than guessed by the decoder, a list
		// key written as one comma-separated string becomes the list the field
		// wants, and a map key written that way becomes the map it wants.
		normalizeDurations(layer.keys)
		normalizeLists(layer.keys)
		normalizeMaps(layer.keys)
		if err := merge(k, cfg.origin, layer.name, layer.keys); err != nil {
			return Config{}, err
		}
	}

	// Unmarshal into the defaults, so a key no source set keeps its default.
	if err := k.Unmarshal("", &cfg); err != nil {
		return Config{}, fmt.Errorf("config: decode: %w", err)
	}

	// The issuer is the deployment's own identity, and the origin it is
	// published at is the value every token consumer can check against. An
	// issuer no source set takes PUBLIC_BASE_URL — the same variable the
	// generated file points both app.base_url and auth.issuer at — and an
	// empty result is Validate's report, not a silent placeholder. The
	// fallback is a resolution rule, not a layer: Origin keeps reporting
	// that no source set the key.
	if cfg.Auth.Issuer == "" {
		if value := lookupValue(table, "PUBLIC_BASE_URL"); value != "" {
			cfg.Auth.Issuer = value
		}
	}
	return cfg, nil
}

// merge applies one layer to the accumulated configuration and records it as the
// origin of the keys it set, replacing an earlier entry. The last layer to set a
// key is the one that won.
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

// Unresolved returns the keys whose interpolation directive named a variable
// that is not set, mapped to the variable name. Such a key keeps its default and
// is reported by Validate, so a command that reads it gets an error naming the
// variable while a command that does not is unaffected.
func (c Config) Unresolved() map[string]string {
	out := make(map[string]string, len(c.unresolved))
	maps.Copy(out, c.unresolved)
	return out
}

// Origin reports which source last set key, or an empty string when no source
// set it. The names are LayerDefault, LayerConfigFile, and LayerFlag.
//
// It makes precedence observable: a key set by both the file and a flag reports
// LayerFlag.
func (c Config) Origin(key string) string {
	return c.origin[key]
}

// Origins returns a copy of the source of every resolved key.
func (c Config) Origins() map[string]string {
	out := make(map[string]string, len(c.origin))
	maps.Copy(out, c.origin)
	return out
}
