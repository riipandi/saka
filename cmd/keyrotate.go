package main

import (
	"context"
	"fmt"
	"io"

	"github.com/urfave/cli/v3"

	fdatastore "github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/envfile"
	"github.com/riipandi/saka/pkg/printext"
)

// The sealed surfaces key:rotate walks. Each entry names its table, the
// sealed column, and the secret whose cipher seals it — the two halves
// (application and auth) rotate together, so the command carries both
// old and both new ciphers. The enc: prefix is what marks a sealed row:
// every CHECK-constrained surface admits no other form, and
// app_settings' plaintext rows are its normal state, so the walk's
// prefix filter is the whole contract, not a heuristic.
type sealedSurface struct {
	label   string
	table   string
	column  string
	keyID   string // the primary key column the report names on failure
	authKey bool   // sealed by the auth cipher, not the application's
	extra   string // a further column the pass updates (jwks' seal_fp)
}

var rotationSurfaces = []sealedSurface{
	{label: "webhook endpoint secrets", table: "public.webhook_endpoints", column: "secret_enc", keyID: "id"},
	{label: "oauth connection client secrets", table: "public.oauth_connections", column: "client_secret", keyID: "id"},
	{label: "scim provider tokens", table: "public.scim_service_providers", column: "token", keyID: "id"},
	{label: "totp enrollment secrets", table: "public.mfa_totp", column: "secret", keyID: "id", authKey: true},
	{label: "jwks private documents", table: "public.jwks", column: "private_key", keyID: "key_id", authKey: true, extra: "seal_fp"},
	{label: "sealed setting values", table: "public.app_settings", column: "value", keyID: "key"},
}

// queuePayloadSurfaces are the queued tasks' sealed payloads, pending and
// archived alike — they join the pass only when the deployment encrypts
// its queue (D3): with the flag off the payloads are plaintext, the
// prefix filter would select nothing, and walking them would lie about
// what the pass covered.
func rotationSurfacesFor(queueEncrypted bool) []sealedSurface {
	surfaces := rotationSurfaces
	if queueEncrypted {
		surfaces = append(append([]sealedSurface{}, rotationSurfaces...),
			sealedSurface{label: "queued task payloads", table: "public.queue_tasks", column: "task", keyID: "id"},
			sealedSurface{label: "archived task payloads", table: "public.queue_tasks_completed", column: "task", keyID: "id"},
		)
	}
	return surfaces
}

var keyRotateCmd = &cli.Command{
	Name:  "key:rotate",
	Usage: "Rotate APP_SECRET_KEY and AUTH_SECRET_KEY, re-sealing every encrypted row",
	Description: `Generates (or takes via --new-key/--new-auth-key) a new pair of
secret keys, re-seals every row the old secrets encrypt under — webhook
signing secrets, OAuth connection client secrets, SCIM provider tokens,
TOTP enrollment secrets, JWKS private documents, sealed setting values,
and (when queue.encrypt is on) the queued task payloads and their
archive — inside one transaction, then writes the new keys to the env
file the deployment already runs.

--dry-run counts the sealed rows and verifies the loaded secrets open
them without writing anything. A row the old secret cannot open aborts
the rotation with the row named, leaving zero writes.

--env-file is the root flag (the root Before loads the configuration
from it — the old secrets come from there), and the same file receives
the new keys: the confirm/--overwrite dance as key:generate applies. A
run without --env-file only prints the new keys.`,
	Hidden: true,
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "overwrite",
			Usage: "Replace existing values in the env file without asking",
		},
		&cli.BoolFlag{
			Name:  "dry-run",
			Usage: "Count the sealed rows and verify the secrets open them; write nothing",
		},
		&cli.StringFlag{
			Name:  "new-key",
			Usage: "An explicit replacement APP_SECRET_KEY (64 hex characters), for scripted runs",
		},
		&cli.StringFlag{
			Name:  "new-auth-key",
			Usage: "An explicit replacement AUTH_SECRET_KEY (hex, HS256 minimum of 32 bytes)",
		},
	},
	Action: runKeyRotate,
}

func runKeyRotate(ctx context.Context, cmd *cli.Command) error {
	cfg, err := fullConfigFrom(ctx)
	if err != nil {
		return err
	}
	out := cmd.Root().Writer

	if cfg.App.SecretKey == "" || cfg.Auth.SecretKey == "" {
		return fmt.Errorf("key:rotate: both %s and %s must resolve in the configuration before a rotation",
			crypto.EnvAppSecretKey, crypto.EnvAuthSecretKey)
	}

	// The old ciphers open what the deployment sealed under its running
	// secrets; a failing parse here is a broken deployment, not a silent
	// skip.
	oldApp, err := crypto.NewCipherFromHex(cfg.App.SecretKey)
	if err != nil {
		return fmt.Errorf("key:rotate: %s: %w", crypto.EnvAppSecretKey, err)
	}
	oldAuth, err := crypto.NewAuthCipher(cfg.Auth.SecretKey)
	if err != nil {
		return fmt.Errorf("key:rotate: %s: %w", crypto.EnvAuthSecretKey, err)
	}

	newAppKey, newAuthKey, err := replacementKeys(cmd)
	if err != nil {
		return err
	}
	newApp, err := crypto.NewCipherFromHex(newAppKey)
	if err != nil {
		return fmt.Errorf("key:rotate: new %s: %w", crypto.EnvAppSecretKey, err)
	}
	newAuth, err := crypto.NewAuthCipher(newAuthKey)
	if err != nil {
		return fmt.Errorf("key:rotate: new %s: %w", crypto.EnvAuthSecretKey, err)
	}

	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Shutdown(context.Background())

	surfaces := rotationSurfacesFor(cfg.Queue.Encrypt)
	if !cfg.Queue.Encrypt {
		fmt.Fprintf(out, "queue.encrypt is off — the queued payloads rest plaintext and stay out of the pass\n")
	}

	if cmd.Bool("dry-run") {
		return dryRunRotation(out, store, oldApp, oldAuth, surfaces)
	}

	summary, err := rotateRows(ctx, store, surfaces, oldApp, oldAuth, newApp, newAuth)
	if err != nil {
		return err
	}

	if err := writeRotatedKeys(cmd, newAppKey, newAuthKey); err != nil {
		// The database has committed; the keys are recoverable from this
		// report, so a failed file write is not a failed rotation — but it
		// must be loud, because a deployment that misses the file boots
		// under the old secret and reads nothing.
		fmt.Fprintf(out, "\nthe database rotated, but writing the env file failed: %s\n", err)
		return printRotatedKeys(out, newAppKey, newAuthKey)
	}

	return printRotationSummary(out, surfaces, summary, newAppKey, newAuthKey)
}

// replacementKeys resolves the two new secrets: an explicit flag wins,
// a fresh generation fills the rest. The generation is the default — an
// operator never types a key — while the flags exist for the scripted
// run that must reproduce a known value.
func replacementKeys(cmd *cli.Command) (appKey, authKey string, err error) {
	appKey = cmd.String("new-key")
	if appKey == "" {
		if appKey, err = crypto.GenerateKeyHex(); err != nil {
			return "", "", fmt.Errorf("key:rotate: generate %s: %w", crypto.EnvAppSecretKey, err)
		}
	}
	authKey = cmd.String("new-auth-key")
	if authKey == "" {
		if authKey, err = crypto.GenerateKeyHex(); err != nil {
			return "", "", fmt.Errorf("key:rotate: generate %s: %w", crypto.EnvAuthSecretKey, err)
		}
	}
	return appKey, authKey, nil
}

// dryRunRotation counts each surface's sealed rows and verifies the old
// ciphers open them, writing nothing. Its report is the operator's go
// signal: the counts and the key check precede any write.
func dryRunRotation(out io.Writer, store *fdatastore.Postgres, oldApp, oldAuth *crypto.Cipher, surfaces []sealedSurface) error {
	counts, err := walkSurfaces(context.Background(), store, surfaces, oldApp, oldAuth, func(ctx context.Context, tx fdatastore.Querier, surface sealedSurface) (int, error) {
		return verifySurface(ctx, tx, surface, oldApp, oldAuth)
	})
	if err != nil {
		return err
	}
	printSurfaceCounts(out, "would re-seal", surfaces, counts)
	fmt.Fprintf(out, "\nthe loaded secrets open every sealed row — no writes were made\n")
	return nil
}

// rotateRows re-seals every surface inside one transaction and aborts on
// the first row the old ciphers cannot open — a wrong key or an already
// rotated row is the operator's information, not a partial state.
func rotateRows(ctx context.Context, store *fdatastore.Postgres, surfaces []sealedSurface, oldApp, oldAuth, newApp, newAuth *crypto.Cipher) (map[string]int, error) {
	return walkSurfaces(ctx, store, surfaces, oldApp, oldAuth, func(ctx context.Context, tx fdatastore.Querier, surface sealedSurface) (int, error) {
		return resealSurface(ctx, tx, surface, oldApp, oldAuth, newApp, newAuth)
	})
}

// walkSurfaces runs one pass over every surface inside one transaction.
// The pass function decides whether the walk verifies (dry run) or
// re-seals (the run); the transaction is the caller's all-or-nothing
// boundary either way.
func walkSurfaces(
	ctx context.Context,
	store *fdatastore.Postgres,
	surfaces []sealedSurface,
	oldApp, oldAuth *crypto.Cipher,
	pass func(ctx context.Context, tx fdatastore.Querier, surface sealedSurface) (int, error),
) (map[string]int, error) {
	summary := make(map[string]int, len(surfaces))
	err := store.WithTx(ctx, func(ctx context.Context, tx fdatastore.Querier) error {
		for _, surface := range surfaces {
			count, err := pass(ctx, tx, surface)
			if err != nil {
				return err
			}
			summary[surface.label] = count
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return summary, nil
}

// verifySurface counts the rows a rotation would touch and proves the
// matching cipher opens them, writing nothing.
func verifySurface(ctx context.Context, tx fdatastore.Querier, surface sealedSurface, oldApp, oldAuth *crypto.Cipher) (int, error) {
	old := oldApp
	if surface.authKey {
		old = oldAuth
	}

	rows, err := sealedRows(ctx, tx, surface)
	if err != nil {
		return 0, err
	}
	sealed, err := bufferRows(rows, surface)
	if err != nil {
		return 0, err
	}

	for _, row := range sealed {
		if _, err := old.Decrypt(string(row.value)); err != nil {
			return len(sealed), fmt.Errorf("key:rotate: %s %s=%s: %w", surface.label, surface.keyID, row.id, err)
		}
	}
	return len(sealed), nil
}

// resealSurface walks one surface's sealed rows and re-seals them in
// place: decrypt under the old cipher, encrypt under the new, update the
// sealed column — and the fingerprint column where the row carries one.
// The walk buffers its rows before updating: a connection cannot run an
// UPDATE while its SELECT cursor is still open.
func resealSurface(ctx context.Context, tx fdatastore.Querier, surface sealedSurface, oldApp, oldAuth, newApp, newAuth *crypto.Cipher) (int, error) {
	old, new := oldApp, newApp
	if surface.authKey {
		old, new = oldAuth, newAuth
	}

	rows, err := sealedRows(ctx, tx, surface)
	if err != nil {
		return 0, err
	}
	sealed, err := bufferRows(rows, surface)
	if err != nil {
		return 0, err
	}

	for _, row := range sealed {
		resealed, err := crypto.ReSeal(old, new, string(row.value))
		if err != nil {
			return len(sealed), fmt.Errorf("key:rotate: %s %s=%s: %w", surface.label, surface.keyID, row.id, err)
		}
		if surface.extra != "" {
			_, err = tx.Exec(ctx, "UPDATE "+surface.table+" SET "+surface.column+" = $1, "+
				surface.extra+" = $2 WHERE "+surface.keyID+" = $3", []byte(resealed), new.Fingerprint(), row.id)
		} else {
			_, err = tx.Exec(ctx, "UPDATE "+surface.table+" SET "+surface.column+" = $1 WHERE "+surface.keyID+" = $2", []byte(resealed), row.id)
		}
		if err != nil {
			return len(sealed), fmt.Errorf("key:rotate: update %s %s=%s: %w", surface.label, surface.keyID, row.id, err)
		}
	}
	return len(sealed), nil
}

// sealedRow is one buffered row: its primary key as the report names it
// and the sealed column's raw bytes.
type sealedRow struct {
	id    string
	value []byte
}

// bufferRows drains a surface's SELECT into memory and closes the
// cursor, so the caller's UPDATEs run on a free connection.
func bufferRows(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}, surface sealedSurface) ([]sealedRow, error) {
	defer rows.Close()

	out := []sealedRow{}
	for rows.Next() {
		var row sealedRow
		if err := rows.Scan(&row.id, &row.value); err != nil {
			return nil, fmt.Errorf("key:rotate: scan %s: %w", surface.label, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("key:rotate: walk %s: %w", surface.label, err)
	}
	return out, nil
}

// sealedRows streams one surface's sealed rows: only the enc:-prefixed
// values are sealed data — the CHECK constraints force the prefix on the
// schema's own surfaces, and app_settings' plaintext rows are its normal
// state. The id casts to text so uuid and text primary keys scan alike,
// and the sealed column reads as raw bytes so text and bytea columns
// walk the same path.
func sealedRows(ctx context.Context, tx fdatastore.Querier, surface sealedSurface) (interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}, error) {
	return tx.Query(ctx, "SELECT "+surface.keyID+"::text, "+surface.column+
		" FROM "+surface.table+" WHERE "+surface.column+" LIKE '"+crypto.EncPrefix+"%'")
}

// writeRotatedKeys replaces both secrets in the env file the deployment
// runs — the same file the root's --env-file flag loaded the old ones
// from, and the same confirm/--overwrite dance key:generate applies.
func writeRotatedKeys(cmd *cli.Command, newAppKey, newAuthKey string) error {
	path := cmd.Root().String(config.FlagEnvFile)
	if path == "" {
		return nil
	}
	ok, err := mayWriteEnvFile(cmd, path)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	file, err := envfile.Load(path)
	if err != nil {
		return err
	}
	file.Set(crypto.EnvAppSecretKey, newAppKey)
	file.Set(crypto.EnvAuthSecretKey, newAuthKey)
	return file.Write(path)
}

// printRotatedKeys writes the new secrets as dotenv lines — the same
// shape key:generate prints — so a run whose file write failed still
// leaves the operator the values.
func printRotatedKeys(out io.Writer, newAppKey, newAuthKey string) error {
	_, err := fmt.Fprintf(out, "%s=%s\n%s=%s\n", crypto.EnvAppSecretKey, newAppKey, crypto.EnvAuthSecretKey, newAuthKey)
	return err
}

// printSurfaceCounts lines the surfaces up in a column and reports how
// many rows each would give a pass.
func printSurfaceCounts(out io.Writer, verb string, surfaces []sealedSurface, counts map[string]int) {
	for _, surface := range surfaces {
		fmt.Fprintf(out, "%-36s %s %d %s\n", surface.label, verb, counts[surface.label], printext.Plural(counts[surface.label], "row"))
	}
}

// printRotationSummary reports what moved and what the deployment must
// do next (restart the processes that hold the old secrets).
func printRotationSummary(out io.Writer, surfaces []sealedSurface, summary map[string]int, newAppKey, newAuthKey string) error {
	printSurfaceCounts(out, "re-sealed", surfaces, summary)
	if err := printRotatedKeys(out, newAppKey, newAuthKey); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "\nrestart the serving processes to pick the new secrets up — a process still running on the old ones reads nothing\n")
	return err
}
