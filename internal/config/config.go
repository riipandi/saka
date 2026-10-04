// Package config resolves the application configuration from one source of
// truth, a JSON config file, and the layers that may override it.
//
// The loading engine — the sources, the precedence, the file's environment
// directives — lives in framework/config; this package is the application's
// schema over it: the Config struct defines every key, its default (Default),
// and its rule (Validate), and Load wraps the engine with the schema and the
// application's resolution rules.
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

	fconfig "github.com/riipandi/saka/framework/config"
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
// Load wraps the framework engine with this application's schema — the known
// keys through their defaults, and the value-form vocabularies (durations,
// lists, maps) — and applies the schema's own resolution rules on top.
//
// Load does not validate: a command that needs one key, such as a migration that
// needs only database.url, must not be blocked by a key it never reads. Call
// Validate where the whole configuration is required.
func Load(opts Options) (Config, error) {
	resolved, err := fconfig.Resolve(fconfig.Options{
		ConfigFile: opts.ConfigFile,
		EnvFile:    opts.EnvFile,
		Flags:      opts.Flags,
		Environ:    opts.Environ,
	}, fconfig.Schema{
		Defaults:  DefaultsMap(),
		Durations: durationKeys,
		Lists:     listKeys,
		Maps:      mapKeys,
	})
	if err != nil {
		return Config{}, err
	}

	cfg := Config{}
	if err := resolved.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: decode: %w", err)
	}
	cfg.origin = resolved.Origins()
	cfg.unresolved = resolved.Unresolved()

	// The issuer is the deployment's own identity, and the origin it is
	// published at is the value every token consumer can check against. An
	// issuer no source set takes PUBLIC_BASE_URL — the same variable the
	// generated file points both app.base_url and auth.issuer at — and an
	// empty result is Validate's report, not a silent placeholder. The
	// fallback is a resolution rule, not a layer: Origin keeps reporting
	// that no source set the key.
	if cfg.Auth.Issuer == "" {
		if value := resolved.LookupEnv("PUBLIC_BASE_URL"); value != "" {
			cfg.Auth.Issuer = value
		}
	}
	return cfg, nil
}

// Unresolved returns the keys whose interpolation directive named a variable
// that is not set, mapped to the variable name. Such a key keeps its default and
// is reported by Validate, so a command that reads it gets an error naming the
// variable while a command that does not is unaffected.
func (c Config) Unresolved() map[string]string {
	out := make(map[string]string, len(c.unresolved))
	for key, name := range c.unresolved {
		out[key] = name
	}
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
	for key, layer := range c.origin {
		out[key] = layer
	}
	return out
}
