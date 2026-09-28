package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/huandu/go-sqlbuilder"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"go.jetify.com/typeid"

	"github.com/riipandi/tango/modules/identity/jwks"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/urfave/cli/v3"
)

var jwksGenerateCmd = &cli.Command{
	Name:  "jwks:generate",
	Usage: "Generate a signing key pair for the OAuth provider",
	Description: `Generates an asymmetric signing key pair and stores it as an
active row of public.jwks. The database is the signing authority for the
OIDC/OAuth surface — the configured auth.private_key/auth.public_key pair
stays the internal one — so the provider refuses to serve until this table
holds a key.

Without --algorithm the pair uses ES256. The private half is sealed with
APP_SECRET_KEY before it is stored, the public half is what discovery
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
	if cfg.App.SecretKey == "" {
		return errors.New("jwks: APP_SECRET_KEY is required to seal the private key")
	}
	cipher, err := crypto.NewCipherFromHex(cfg.App.SecretKey)
	if err != nil {
		return fmt.Errorf("jwks: secret key: %w", err)
	}

	key, algorithm, keyType, err := generateSigningKey(cmd.String("algorithm"))
	if err != nil {
		return err
	}

	kid, err := typeid.New[jwks.JWKSKeyID]()
	if err != nil {
		return fmt.Errorf("jwks: key id: %w", err)
	}
	if setErr := key.Set(jwk.KeyIDKey, kid.String()); setErr != nil {
		return fmt.Errorf("jwks: set kid: %w", setErr)
	}
	if setErr := key.Set(jwk.AlgorithmKey, algorithm); setErr != nil {
		return fmt.Errorf("jwks: set alg: %w", setErr)
	}
	if setErr := key.Set(jwk.KeyUsageKey, jwks.KeyUsageSignature); setErr != nil {
		return fmt.Errorf("jwks: set use: %w", setErr)
	}

	private, err := jsonJWK(key)
	if err != nil {
		return err
	}
	sealed, err := cipher.Encrypt(private)
	if err != nil {
		return fmt.Errorf("jwks: seal private key: %w", err)
	}
	publicKey, err := jwk.PublicKeyOf(key)
	if err != nil {
		return fmt.Errorf("jwks: derive public key: %w", err)
	}
	public, err := jsonJWK(publicKey)
	if err != nil {
		return err
	}

	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Shutdown(context.Background())

	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(jwks.TableJWKS)
	sb.Cols("key_id", "algorithm", "key_type", "public_key", "private_key", "use_for", "is_active")
	sb.Values(kid.String(), algorithm, keyType, []byte(public), []byte(sealed), jwks.UseSignature, true)
	query, args := sb.Build()
	if _, err := store.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("jwks: store key: %w", err)
	}

	fmt.Fprintf(cmd.Root().Writer, "status: ok\n")
	fmt.Fprintf(cmd.Root().Writer, "  key_id: %s\n", kid.String())
	fmt.Fprintf(cmd.Root().Writer, "  algorithm: %s\n", algorithm)
	return nil
}

// generateSigningKey builds the raw key pair the algorithm names. A
// symmetric algorithm is refused: a shared secret has no publishable
// half, and this table is the published one.
func generateSigningKey(name string) (jwk.Key, string, string, error) {
	var raw any
	var keyType string
	switch name {
	case "ES256":
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, "", "", fmt.Errorf("jwks: generate key: %w", err)
		}
		raw, keyType = key, "EC"
	case "ES384":
		key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			return nil, "", "", fmt.Errorf("jwks: generate key: %w", err)
		}
		raw, keyType = key, "EC"
	case "RS256":
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, "", "", fmt.Errorf("jwks: generate key: %w", err)
		}
		raw, keyType = key, "RSA"
	default:
		return nil, "", "", fmt.Errorf("jwks: --algorithm %q is not an asymmetric signature algorithm", name)
	}
	key, err := jwk.Import(raw)
	if err != nil {
		return nil, "", "", fmt.Errorf("jwks: build key: %w", err)
	}
	return key, name, keyType, nil
}

// jsonJWK renders a key as its JWK JSON document, the form the row and
// the published set both carry.
func jsonJWK(key jwk.Key) (string, error) {
	document, err := json.Marshal(key)
	if err != nil {
		return "", fmt.Errorf("jwks: encode key: %w", err)
	}
	return string(document), nil
}
