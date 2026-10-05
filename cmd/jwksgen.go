package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/internal/database/seeders"
	"github.com/riipandi/saka/modules/identity/jwks"
	"github.com/riipandi/saka/pkg/crypto"
)

var jwksGenerateCmd = &cli.Command{
	Name:  "jwks:generate",
	Usage: "Generate a signing key pair for the OAuth provider",
	Description: `Generates an asymmetric signing key pair and stores it as an
active row of public.jwks. The database is the application's only signing
authority — the environment carries no key-pair material — so this command
is how a rotation adds a key without touching the deployment. saka
initialize provisions the first pair on a fresh database.

Without --algorithm the pair uses ES256. The private half is sealed with
AUTH_SECRET_KEY before it is stored, the public half is what discovery
publishes. Run the command again to stage a second key for rotation.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "algorithm",
			Usage: "Signature algorithm: ES256, ES384, or RS256",
			Value: "ES256",
		},
	},
	Action: runJwksGenerate,
}

func runJwksGenerate(ctx context.Context, cmd *cli.Command) error {
	cfg, err := configFrom(ctx)
	if err != nil {
		return err
	}
	if cfg.Auth.SecretKey == "" {
		return errors.New("jwks: AUTH_SECRET_KEY is required to seal the private key")
	}
	cipher, err := crypto.NewAuthCipher(cfg.Auth.SecretKey)
	if err != nil {
		return fmt.Errorf("jwks: auth secret: %w", err)
	}

	pair, err := jwks.GeneratePairWith(cmd.String("algorithm"), cipher)
	if err != nil {
		return fmt.Errorf("jwks: %w", err)
	}

	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Shutdown(context.Background())

	if err := seeders.InsertJWKSRow(ctx, store, pair); err != nil {
		return fmt.Errorf("jwks: %w", err)
	}

	fmt.Fprintf(cmd.Root().Writer, "status: ok\n")
	fmt.Fprintf(cmd.Root().Writer, "  key_id: %s\n", pair.KeyID)
	fmt.Fprintf(cmd.Root().Writer, "  algorithm: %s\n", pair.Algorithm)
	return nil
}
