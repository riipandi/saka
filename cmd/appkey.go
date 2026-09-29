package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/envfile"
	"github.com/riipandi/tango/pkg/printext"
	"github.com/urfave/cli/v3"
)

var keyGenerateCmd = &cli.Command{
	Name:  "key:generate",
	Usage: "Generate the application secret keys",
	Description: `Generates APP_SECRET_KEY (AES-256) and AUTH_SECRET_KEY (HMAC).

Without --algorithm the HMAC secret uses HS256; an HS* --algorithm replaces
it. The JWT signing key pair is not generated here — it lives in the
database's public.jwks table, provisioned by tango initialize and rotated by
jwks:generate.

Without --env-file the values are only printed to stdout. With --env-file a
missing file is created; an existing file is updated only after confirmation, or
without asking when --overwrite is passed.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "env-file",
			Usage: "Write the generated keys to this env file instead of printing them",
		},
		&cli.StringFlag{
			Name:  "algorithm",
			Usage: "JWT signature algorithm for the key pair, or an HS* algorithm for AUTH_SECRET_KEY",
		},
		&cli.BoolFlag{
			Name:  "overwrite",
			Usage: "Replace existing values in the env file without asking",
		},
	},
	Action: runKeyGenerate,
}

func runKeyGenerate(_ context.Context, cmd *cli.Command) error {
	generator, err := crypto.NewKeyGenerator(cmd.String("algorithm"))
	if err != nil {
		return err
	}
	keys, err := generator.Generate()
	if err != nil {
		return err
	}

	out := cmd.Root().Writer
	path := cmd.String("env-file")
	// File access, including the confirmation prompt, happens only when
	// --env-file is passed. Without it the command prints and stops.
	if path == "" {
		return printSecretKeys(out, keys)
	}

	write, err := mayWriteEnvFile(cmd, path)
	if err != nil {
		return err
	}
	if !write {
		if err := printSecretKeys(out, keys); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "\n%s left unchanged\n", path)
		return err
	}

	secret := generator.Algorithms()
	return writeSecretKeys(out, path, secret, keys)
}

// mayWriteEnvFile reports whether the env file may be written. A missing
// file is created; an existing file needs --overwrite or a confirmation.
func mayWriteEnvFile(cmd *cli.Command, path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("inspect %s: %w", path, err)
	case cmd.Bool("overwrite"):
		return true, nil
	default:
		return confirmOverwrite(cmd, path)
	}
}

// confirmOverwrite asks before replacing the values of an existing file.
// Without an answer (empty input, EOF, or a non-interactive run) the file
// is left alone.
func confirmOverwrite(cmd *cli.Command, path string) (bool, error) {
	out := cmd.Root().Writer
	if _, err := fmt.Fprintf(out, "%s already exists — replace its key values? [y/N] ", path); err != nil {
		return false, err
	}

	in := cmd.Root().Reader
	if in == nil {
		in = os.Stdin
	}
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && answer == "" {
		if _, writeErr := fmt.Fprintln(out); writeErr != nil {
			return false, writeErr
		}
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// printSecretKeys writes the generated variables as dotenv lines.
func printSecretKeys(out io.Writer, keys crypto.GeneratedKeys) error {
	for _, name := range keys.Names() {
		if _, err := fmt.Fprintf(out, "%s=%s\n", name, keys[name]); err != nil {
			return err
		}
	}
	return nil
}

// writeSecretKeys replaces the generated variables in the env file and
// appends the ones that are missing, printing the values and a short
// summary.
func writeSecretKeys(out io.Writer, path, secret string, keys crypto.GeneratedKeys) error {
	file, err := envfile.Load(path)
	if err != nil {
		return err
	}

	var added, replaced int
	for _, name := range keys.Names() {
		if file.Set(name, keys[name]) {
			replaced++
		} else {
			added++
		}
	}
	if err := file.Write(path); err != nil {
		return err
	}
	if err := printSecretKeys(out, keys); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(out, "\nwrote %s (%d added, %d replaced; secret %s)\n",
		path, added, replaced, secret); err != nil {
		return err
	}
	if replaced > 0 {
		if _, err := fmt.Fprintf(out,
			"warning: replaced %d existing %s — data encrypted or signed with the previous keys is no longer readable\n",
			replaced, printext.Plural(replaced, "key")); err != nil {
			return err
		}
	}
	return nil
}

var keyRotateCmd = &cli.Command{
	Name:  "key:rotate",
	Usage: "Rotate encryption keys and re-encrypts data",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		fmt.Println("not yet implemented")
		return nil
	},
}
