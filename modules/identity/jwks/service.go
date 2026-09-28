// Package jwks publishes the JSON Web Key Set the application signs with and
// verifies against.
//
// Signing is dual stack. The configuration holds an asymmetric key pair
// (`auth.private_key` / `auth.public_key`) and an HMAC secret
// (`auth.secret_key`); which one signs a given token is the caller's choice,
// made where the token is created. The database holds the keys of a deployment
// that acts as an OAuth provider, which is how a further key joins the set
// without a redeploy.
//
// Only the asymmetric keys are published. A JWKS is a public document, and a
// symmetric key's "public" form is the secret itself, so the HMAC secret
// verifies locally and never leaves the process. That is what the HS* half of
// the dual stack costs, and it is the same trade every deployment makes.
//
// The service satisfies jwtutils.KeyProvider, so the endpoint that publishes
// the set and the code that verifies a token read the same source: a key that
// is not published is not accepted, and a key that is published is accepted
// without a second list to keep in step.
package jwks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/pkg/crypto"
)

// ErrNoSigningKey reports a configuration with no key to sign with. It is
// returned by the accessor for the stack the deployment did not configure,
// not refused at start-up: a run with only the key pair is a valid one, and so
// is a run with only the HMAC secret.
var ErrNoSigningKey = errors.New("jwks: no signing key configured")

// KeyCacheTTL is how long a built key set is reused before the source is read
// again. It is short because the value is cheap to rebuild and a rotation
// must be picked up promptly; it is not zero because a client that verifies
// many tokens must not turn each verification into a query. The HTTP
// `max-age` a client honours is a separate, longer window.
const KeyCacheTTL = time.Minute

// Source reads the published keys a deployment stores itself.
type Source interface {
	ActiveSigningKeys(ctx context.Context) ([]StoredKey, error)
}

// PairSource is the optional extension a Source implements when its rows
// carry a sealed private key: the read the OAuth provider signs from. The
// publishing path keeps the public-only contract of Source.
type PairSource interface {
	ActiveSigningKeyPairs(ctx context.Context) ([]SigningKeyPair, error)
}

// ErrNoStoredKeys reports a source that carries no signing pairs, or no
// source at all. The OAuth provider refuses to run on it: minting tokens
// with the configured key while the deployment meant the database to sign
// would put a key in the published set that no row explains.
var ErrNoStoredKeys = errors.New("jwks: no signing key pairs in the database; generate one with tango jwks:generate")

// Service owns the published key set and the configured signing material.
type Service struct {
	source Source
	log    *slog.Logger

	// once guards the parse of the configured keys: they are read from
	// strings that do not change for the life of the process, and a parse
	// failure is remembered rather than retried on every request.
	once sync.Once

	// privateKey and publicKey are the asymmetric half of the dual stack.
	privateKey jwk.Key
	publicKey  jwk.Key

	// hmacKey is the symmetric half, from auth.secret_key. It signs and
	// verifies locally and is never published.
	hmacKey jwk.Key

	// cipher unseals a stored signing pair's private key for the OAuth
	// provider. A run without a secret key has nothing to unseal; the
	// OIDC signing keys fail closed on it.
	cipher *crypto.Cipher

	// configured is auth.jwt_algorithm, empty when the deployment lets the
	// material decide.
	configured string

	parseErr error
}

// NewService builds the service. source may be nil, which is a run with no
// OAuth provider tables to read: the published set is then the configured keys
// alone. cipher may be nil — a run without a secret key cannot unseal a
// stored private key, and the OIDC signing keys fail closed on it.
func NewService(cfg config.Config, source Source, cipher *crypto.Cipher, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	service := &Service{source: source, cipher: cipher, log: log}
	// The parse runs here rather than on the first request: a key that cannot
	// be read is a broken deployment, and it should be reported before the
	// listener opens instead of as a 500 on a client's first verification.
	service.parse(cfg)
	return service
}

// parse reads the configured keys once. A missing half is not an error — a
// deployment may configure either stack — but a value that is present and
// unreadable is.
func (s *Service) parse(cfg config.Config) {
	s.once.Do(func() {
		s.configured = cfg.Auth.JWTAlgorithm
		if err := s.parseKeyPair(cfg); err != nil {
			s.parseErr = err
			return
		}
		s.parseHMAC(cfg)
		if s.parseErr != nil {
			return
		}
		s.parseErr = s.checkConfiguredAlgorithm()
	})
}

// checkConfiguredAlgorithm reports a named algorithm whose material is
// missing. Configuration validation refuses the same mismatch, so a validated
// run never reaches here; the check exists because the service is also built
// directly, and signing with a key the deployment did not configure is a
// failure worth reporting at construction rather than at the first token.
func (s *Service) checkConfiguredAlgorithm() error {
	if s.configured == "" {
		return nil
	}
	alg, ok := jwa.LookupSignatureAlgorithm(s.configured)
	if !ok {
		return fmt.Errorf("jwks: auth.jwt_algorithm: %q is not a signature algorithm", s.configured)
	}
	if alg.IsSymmetric() && s.hmacKey == nil {
		return fmt.Errorf("jwks: auth.jwt_algorithm: %q requires auth.secret_key", s.configured)
	}
	if !alg.IsSymmetric() && s.privateKey == nil {
		return fmt.Errorf("jwks: auth.jwt_algorithm: %q requires auth.private_key", s.configured)
	}
	return nil
}

// parseKeyPair reads the asymmetric half.
func (s *Service) parseKeyPair(cfg config.Config) error {
	if cfg.Auth.PublicKey == "" {
		return nil
	}
	public, err := crypto.DecodeJWK(cfg.Auth.PublicKey)
	if err != nil {
		return fmt.Errorf("jwks: auth.public_key: %w", err)
	}
	// A symmetric key has no public half: its "public" form is the secret
	// itself, so publishing it would disclose the signing key. An HS*
	// deployment signs with auth.secret_key, which is never published.
	if symErr := rejectSymmetric(public); symErr != nil {
		return fmt.Errorf("jwks: auth.public_key: %w", symErr)
	}
	// The `use` is stamped here, once, rather than at publish time: the key
	// is shared by every request, so mutating it while serving would be a
	// data race.
	if setErr := public.Set(jwk.KeyUsageKey, KeyUsageSignature); setErr != nil {
		return fmt.Errorf("jwks: auth.public_key: set use: %w", setErr)
	}
	s.publicKey = public

	if cfg.Auth.PrivateKey == "" {
		return nil
	}
	private, err := crypto.DecodeJWK(cfg.Auth.PrivateKey)
	if err != nil {
		return fmt.Errorf("jwks: auth.private_key: %w", err)
	}
	if symErr := rejectSymmetric(private); symErr != nil {
		return fmt.Errorf("jwks: auth.private_key: %w", symErr)
	}
	// The private key is the one the published key was derived from. A pair
	// that does not agree would sign tokens no client could verify, so it is
	// refused here rather than discovered by a rejected token.
	if err := keyPairAgrees(private, public); err != nil {
		return fmt.Errorf("jwks: auth.private_key: %w", err)
	}
	s.privateKey = private
	return nil
}

// parseHMAC reads the symmetric half.
func (s *Service) parseHMAC(cfg config.Config) {
	if cfg.Auth.SecretKey == "" {
		return
	}
	// The secret is hex, the form key:generate writes. Its length follows the
	// algorithm (32, 48, or 64 bytes), so it does not go through the AES-256
	// reader that insists on exactly 32.
	raw, err := crypto.ParseHMACKeyHex(cfg.Auth.SecretKey)
	if err != nil {
		s.parseErr = fmt.Errorf("jwks: auth.secret_key: %w", err)
		return
	}
	key, err := jwk.Import(raw)
	if err != nil {
		s.parseErr = fmt.Errorf("jwks: auth.secret_key: %w", err)
		return
	}
	if setErr := key.Set(jwk.KeyUsageKey, KeyUsageSignature); setErr != nil {
		s.parseErr = fmt.Errorf("jwks: auth.secret_key: set use: %w", setErr)
		return
	}
	s.hmacKey = key
}

// keyPairAgrees reports whether the public key is the one the private key
// yields. A private key carries its own public part, so the comparison is
// local and needs no configuration.
func keyPairAgrees(private, public jwk.Key) error {
	derived, err := jwk.PublicKeyOf(private)
	if err != nil {
		return fmt.Errorf("derive public key: %w", err)
	}
	if !jwk.Equal(derived, public) {
		return errors.New("does not match auth.public_key")
	}
	return nil
}

// SigningAlgorithm reports the algorithm a token is signed with.
//
// The configured auth.jwt_algorithm wins when it is set: that is the value a
// deployment gives when it configures both stacks, and the only way it can say
// which one signs. When it is unset the answer is derived — the key pair's own
// `alg` if there is a key pair, the HMAC secret's length otherwise — so a
// deployment with one stack never has to state what its material already says.
func (s *Service) SigningAlgorithm() (jwa.SignatureAlgorithm, error) {
	if s.parseErr != nil {
		return jwa.NoSignature(), s.parseErr
	}
	if s.configured != "" {
		alg, ok := jwa.LookupSignatureAlgorithm(s.configured)
		if !ok {
			return jwa.NoSignature(), fmt.Errorf("jwks: auth.jwt_algorithm: %q is not a signature algorithm", s.configured)
		}
		// The named algorithm must match the material. A symmetric one needs
		// the secret; an asymmetric one needs the key pair. Configuration
		// validation refuses the mismatch at start-up, so reaching here is a
		// caller that built a Service without validating.
		if alg.IsSymmetric() && s.hmacKey == nil {
			return jwa.NoSignature(), fmt.Errorf("jwks: auth.jwt_algorithm: %q requires auth.secret_key", s.configured)
		}
		if !alg.IsSymmetric() && s.privateKey == nil {
			return jwa.NoSignature(), fmt.Errorf("jwks: auth.jwt_algorithm: %q requires auth.private_key", s.configured)
		}
		return alg, nil
	}

	if s.privateKey != nil {
		alg, ok := s.privateKey.Algorithm()
		if !ok {
			// The generator always stamps `alg`, so a key pair without one was
			// written by hand. The curve determines the algorithm, but the
			// mapping lives in an internal jwx package, so rather than
			// restating it here the deployment is asked to say what it means.
			return jwa.NoSignature(), errors.New(
				"jwks: auth.private_key carries no alg; set auth.jwt_algorithm")
		}
		looked, ok := jwa.LookupSignatureAlgorithm(alg.String())
		if !ok {
			return jwa.NoSignature(), fmt.Errorf("jwks: auth.private_key: %q is not a signature algorithm", alg)
		}
		return looked, nil
	}
	if s.hmacKey != nil {
		return s.HMACAlgorithm()
	}
	return jwa.NoSignature(), ErrNoSigningKey
}

// Err reports the parse failure of the configured keys, if any.
func (s *Service) Err() error { return s.parseErr }

// SignKey returns the asymmetric private key, the default for stateless JWTs.
// It is the key a client verifies against the published set, so it is what a
// token meant for an outside caller is signed with.
func (s *Service) SignKey(context.Context) (jwk.Key, error) {
	if s.parseErr != nil {
		return nil, s.parseErr
	}
	if s.privateKey == nil {
		return nil, ErrNoSigningKey
	}
	return s.privateKey, nil
}

// HMACKey returns the symmetric signing key. It is the other half of the dual
// stack: a token signed with it is verified by the same process, using
// auth.secret_key, and is never verifiable from the published set — a
// symmetric key cannot be published without disclosing it.
func (s *Service) HMACKey(context.Context) (jwk.Key, error) {
	if s.parseErr != nil {
		return nil, s.parseErr
	}
	if s.hmacKey == nil {
		return nil, ErrNoSigningKey
	}
	return s.hmacKey, nil
}

// HMACAlgorithm returns the algorithm the configured HMAC secret is used
// with, chosen by its length. The mapping lives in
// crypto.HMACAlgorithmForSecret — the same rule key:generate follows when it
// sizes the secret — so the two ends of AUTH_SECRET_KEY cannot disagree.
func (s *Service) HMACAlgorithm() (jwa.SignatureAlgorithm, error) {
	key, err := s.HMACKey(context.Background())
	if err != nil {
		return jwa.NoSignature(), err
	}
	secret, ok := key.(jwk.SymmetricKey)
	if !ok {
		return jwa.NoSignature(), fmt.Errorf("jwks: auth.secret_key: not a symmetric key")
	}
	octets, ok := secret.Octets()
	if !ok {
		return jwa.NoSignature(), fmt.Errorf("jwks: auth.secret_key: no key material")
	}
	name, ok := crypto.HMACAlgorithmForSecret(len(octets))
	if !ok {
		return jwa.NoSignature(), fmt.Errorf(
			"jwks: auth.secret_key: %d bytes names no HMAC algorithm (32, 48, or 64)", len(octets))
	}
	alg, ok := jwa.LookupSignatureAlgorithm(name)
	if !ok {
		return jwa.NoSignature(), fmt.Errorf("jwks: auth.secret_key: %q is not a signature algorithm", name)
	}
	return alg, nil
}

// VerifyKeySet returns the public keys a token may be verified against: the
// configured public key first, then every active signing key the database
// holds.
//
// The HMAC secret is deliberately absent. A JWKS is a public document, and a
// symmetric key's "public" form is the secret itself, so publishing it would
// hand every reader the ability to mint tokens. A caller that must verify an
// HS* token takes HMACKey instead.
//
// A database that cannot be read degrades the set to the configured key
// rather than failing the call. The configured key is the one the application
// signs with, so a caller verifying its own token still succeeds while the
// provider tables are unavailable; the alternative — refusing every
// verification because a future feature's table is unreachable — would turn a
// partial outage into a total one. The failure is logged, not swallowed.
func (s *Service) VerifyKeySet(ctx context.Context) (jwk.Set, error) {
	if s.parseErr != nil {
		return nil, s.parseErr
	}

	set := jwk.NewSet()
	seen := make(map[string]struct{}, 1)

	if s.publicKey != nil {
		if err := set.AddKey(s.publicKey); err != nil {
			return nil, fmt.Errorf("jwks: add configured key: %w", err)
		}
		if kid, ok := s.publicKey.KeyID(); ok {
			seen[kid] = struct{}{}
		}
	}

	stored, err := s.storedKeys(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "jwks: reading stored keys failed; publishing the configured key alone", "err", err)
		return set, nil
	}

	for _, key := range stored {
		// The configured key is the default, so a database row carrying the
		// same `kid` does not replace it and does not appear twice: a set
		// with one key named twice is a set a client cannot index.
		if _, duplicate := seen[key.KeyID]; duplicate {
			continue
		}
		parsed, err := parseStoredKey(key)
		if err != nil {
			// One unusable row must not take the whole set down: the other
			// keys are still the ones a client needs.
			s.log.ErrorContext(ctx, "jwks: skipping an unusable stored key",
				"kid", key.KeyID, "err", err)
			continue
		}
		if err := set.AddKey(parsed); err != nil {
			return nil, fmt.Errorf("jwks: add stored key %s: %w", key.KeyID, err)
		}
		seen[key.KeyID] = struct{}{}
	}
	return set, nil
}

// storedKeys reads the database keys, or nothing when no source is wired.
func (s *Service) storedKeys(ctx context.Context) ([]StoredKey, error) {
	if s.source == nil {
		return nil, nil
	}
	return s.source.ActiveSigningKeys(ctx)
}

// OIDCSigningKeys reads the database's active signing key pairs and parses
// them into keys that carry their private material, so the OAuth provider
// can both sign with them and publish their public halves. The `kid`,
// `alg`, and `use` members are stamped here: a token names the kid to
// select the key, and introspection refuses a key without the usage.
//
// The database is the OIDC signing authority — the configured key pair is
// the internal one — so an empty or unwired source fails closed. A row
// whose sealed private key cannot be opened is skipped with an error log;
// one unusable row must not take the provider down.
func (s *Service) OIDCSigningKeys(ctx context.Context) ([]jwk.Key, error) {
	if s.parseErr != nil {
		return nil, s.parseErr
	}
	pairs, ok := s.source.(PairSource)
	if !ok || pairs == nil {
		return nil, ErrNoStoredKeys
	}

	rows, err := pairs.ActiveSigningKeyPairs(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNoStoredKeys
	}

	keys := make([]jwk.Key, 0, len(rows))
	for _, row := range rows {
		opened, err := s.openSealedKey(ctx, row)
		if err != nil {
			s.log.ErrorContext(ctx, "jwks: skipping an unusable signing pair",
				"kid", row.KeyID, "err", err)
			continue
		}
		if err := opened.Set(jwk.KeyIDKey, row.KeyID); err != nil {
			return nil, fmt.Errorf("jwks: set kid: %w", err)
		}
		if row.Algorithm != "" {
			if err := opened.Set(jwk.AlgorithmKey, row.Algorithm); err != nil {
				return nil, fmt.Errorf("jwks: set alg: %w", err)
			}
		}
		if err := opened.Set(jwk.KeyUsageKey, KeyUsageSignature); err != nil {
			return nil, fmt.Errorf("jwks: set use: %w", err)
		}
		keys = append(keys, opened)
	}
	if len(keys) == 0 {
		return nil, ErrNoStoredKeys
	}
	return keys, nil
}

// openSealedKey unseals one row's private key and parses it. A symmetric
// result is refused: a shared secret has no place in a published set.
func (s *Service) openSealedKey(ctx context.Context, row SigningKeyPair) (jwk.Key, error) {
	if s.cipher == nil {
		return nil, errors.New("no secret key is configured to unseal the private key")
	}
	opened, err := s.cipher.Decrypt(string(row.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("unseal private key: %w", err)
	}
	parsed, err := jwk.ParseKey([]byte(opened))
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	if symErr := rejectSymmetric(parsed); symErr != nil {
		return nil, symErr
	}
	return parsed, nil
}

// ErrSymmetricKey reports a key that cannot be published. A symmetric key has
// no public half — its "public" form is the secret itself — so a JWKS that
// carried one would hand every client the key it signs with.
var ErrSymmetricKey = errors.New("jwks: a symmetric key cannot be published")

// rejectSymmetric refuses a key whose type is a shared secret. It guards both
// paths that feed the published set: the configured key pair and a stored row.
// HMAC signing is supported — it uses auth.secret_key, which is never
// published — so this rejects publication, not the algorithm.
func rejectSymmetric(key jwk.Key) error {
	if key.KeyType() == jwa.OctetSeq() {
		return fmt.Errorf("%w (kty=%s)", ErrSymmetricKey, key.KeyType())
	}
	return nil
}

// parseStoredKey turns one stored row into a public JWK.
//
// The stored public key is JWK JSON, which is what the OAuth provider work
// writes. The private key column is never read, so a row that carries one
// cannot leak it here. The `kid`, `alg`, and `use` the row declares are
// stamped onto the key: a published JWK without them is one a client cannot
// match to a token header.
func parseStoredKey(key StoredKey) (jwk.Key, error) {
	if key.KeyID == "" {
		return nil, errors.New("key id is empty")
	}
	parsed, err := jwk.ParseKey(key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	if symErr := rejectSymmetric(parsed); symErr != nil {
		return nil, symErr
	}

	// A stored private key would parse as a private JWK; the public half is
	// what the set carries, so the private fields are dropped here whatever
	// the row holds.
	public, err := jwk.PublicKeyOf(parsed)
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}
	if err := public.Set(jwk.KeyIDKey, key.KeyID); err != nil {
		return nil, fmt.Errorf("set kid: %w", err)
	}
	if key.Algorithm != "" {
		if err := public.Set(jwk.AlgorithmKey, key.Algorithm); err != nil {
			return nil, fmt.Errorf("set alg: %w", err)
		}
	}
	if err := public.Set(jwk.KeyUsageKey, KeyUsageSignature); err != nil {
		return nil, fmt.Errorf("set use: %w", err)
	}
	return public, nil
}
