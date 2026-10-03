package config

import (
	"slices"
	"strings"

	"github.com/knadh/koanf/providers/cliflagv3"
	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/pkg/envfile"
)

// Flag names the root command declares. They are the plumbing of the config
// layer itself, so they are named here rather than at the call site.
const (
	FlagConfigFile = "config-file"
	FlagEnvFile    = "env-file"
)

// flagBindings maps a command-line flag name to the config key it sets. Only a
// flag listed here reaches the configuration, and a flag left unset is absent
// from the layer, so it cannot override the config file with a value nobody
// typed.
//
// A flag belongs here when it names a setting the config file also holds and the
// user is expected to try a value for one run: `serve --port=9000` overrides
// server.port without editing the file. A flag that changes what a command does,
// such as --dry-run or --force, is not listed: it is behavior, not
// configuration, and must never become a key.
var flagBindings = map[string]string{
	"host":     "server.host",
	"port":     "server.port",
	"base-url": "app.base_url",
}

// FromCommand builds the options Load merges from a parsed command, so a caller
// gets the full precedence chain without restating it.
func FromCommand(cmd *cli.Command) (Options, error) {
	opts := Options{
		ConfigFile: cmd.String(FlagConfigFile),
		Flags:      flagLayer(cmd),
	}

	if path := cmd.String(FlagEnvFile); path != "" {
		values, err := readEnvFile(path)
		if err != nil {
			return Options{}, err
		}
		opts.EnvFile = values
	}

	if len(opts.Flags) == 0 {
		opts.Flags = nil
	}
	return opts, nil
}

// Resolve loads the configuration for a parsed command. It is the entry point a
// command action calls.
func Resolve(cmd *cli.Command) (Config, error) {
	opts, err := FromCommand(cmd)
	if err != nil {
		return Config{}, err
	}
	return Load(opts)
}

// flagLayer reads the flags the user actually set, at every level of the command
// chain, and maps them to config keys.
//
// Every level is read because a flag belongs to the command that declares it: the
// root command owns --config-file, and a subcommand owns its own flags such as
// --host.
func flagLayer(cmd *cli.Command) map[string]any {
	out := make(map[string]any)
	levels := lineage(cmd)
	if sub := executingCommand(cmd); sub != nil {
		levels = append(levels, sub)
	}
	for _, level := range levels {
		for name, value := range readFlags(level) {
			key, ok := flagBindings[name]
			if !ok {
				continue
			}
			out[key] = value
		}
	}
	return out
}

// executingCommand returns the subcommand the run names, or nil when the run is
// the root command itself.
//
// A root Before receives the root command, whose lineage stops at the root: the
// flags a subcommand owns are parsed on the child the first positional argument
// names, and without this walk they are invisible to the layer. One hop only —
// no subcommand of this application nests another.
func executingCommand(cmd *cli.Command) *cli.Command {
	name := cmd.Args().First()
	if name == "" || name == cmd.Name {
		return nil
	}
	for _, sub := range cmd.Commands {
		if sub.Name == name {
			return sub
		}
	}
	return nil
}

// readFlags returns the flags set on one command, keyed by flag name.
//
// The provider nests its result under the command path, so the keys are stripped
// back to the flag names the bindings are written in. The path is built from the
// same lineage the provider walked, so the two cannot disagree.
func readFlags(cmd *cli.Command) map[string]any {
	flat, err := cliflagv3.Provider(cmd, Delim).Read()
	if err != nil {
		// The provider only fails when its output cannot be unflattened, which
		// would be a bug in this package rather than a user error.
		return nil
	}

	prefix := strings.Join(commandPath(cmd), Delim) + Delim
	out := make(map[string]any, len(flat))
	for key, value := range flattenNested(flat, nil) {
		name, ok := strings.CutPrefix(key, prefix)
		if !ok || name == "" {
			continue
		}
		out[name] = value
	}
	return out
}

// lineage returns the command chain from the root to cmd, the order the provider
// walks it.
func lineage(cmd *cli.Command) []*cli.Command {
	chain := cmd.Lineage()
	out := make([]*cli.Command, 0, len(chain))
	for _, c := range slices.Backward(chain) {
		out = append(out, c)
	}
	return out
}

// commandPath returns the names of a command and its ancestors, root first.
func commandPath(cmd *cli.Command) []string {
	chain := lineage(cmd)
	path := make([]string, 0, len(chain))
	for _, level := range chain {
		path = append(path, level.Name)
	}
	return path
}

// readEnvFile parses a dotenv file into key-value pairs. The file is read with
// pkg/envfile, so quoting and the `export` prefix behave the same here as they
// do in key:generate.
func readEnvFile(path string) (map[string]string, error) {
	file, err := envfile.Load(path)
	if err != nil {
		return nil, err
	}

	values := make(map[string]string, len(file.Keys()))
	for _, key := range file.Keys() {
		if value, ok := file.Get(key); ok {
			values[key] = value
		}
	}
	return values, nil
}
