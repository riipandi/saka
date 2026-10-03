// Package jwks owns the JSON Web Key Set the application signs with and
// verifies against.
//
// The database is the one signing authority. Every asymmetric key — the one
// the internal surfaces sign with and the ones the OAuth provider offers —
// lives in public.jwks, its private half sealed with the application secret.
// saka initialize provisions the first key pair; jwks:generate stages a
// further one for rotation. The configuration carries no key-pair material.
//
// Signing stays dual stack. The HMAC secret (auth.secret_key) signs tokens
// that are verified by this process alone and never published — a JWKS that
// carried a symmetric key would hand every reader the ability to mint
// tokens. Which stack signs a given token is the caller's choice, made
// where the token is created.
//
// The service satisfies jwtutils.KeyProvider, so the endpoint that publishes
// the set and the code that verifies a token read the same source: a key that
// is not published is not accepted, and a key that is published is accepted
// without a second list to keep in step.
package jwks

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/crypto"
)

// ErrNoSigningKey reports a deployment whose signing material is missing. The
// asymmetric half comes from the database and the symmetric half from the
// configuration, so the message names both doors: saka initialize provisions
// the first key pair, key:generate writes the HMAC secret.
var ErrNoSigningKey = errors.New("jwks: no signing key: run saka initialize to provision a signing key pair, or key:generate to write auth.secret_key")

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
// carry a sealed private key: the read every signing walks. The publishing
// path keeps the public-only contract of Source.
type PairSource interface {
	ActiveSigningKeyPairs(ctx context.Context) ([]SigningKeyPair, error)
}

// ErrNoStoredKeys reports a source that carries no signing pairs, or no
// source at all. Nothing in the process can mint a token without one.
var ErrNoStoredKeys = errors.New("jwks: no signing key pair in the database; run saka initialize to provision one")

// Sealer is the write seam the auto-invalidation runs through. The
// repository implements it; the interface keeps a test able to stand in.
// It retires rows sealed by a key the process no longer holds and stages
// the replacement pair the rotation needs. Querier exposes the write
// surface the audit record rides, so the invalidation is recorded on the
// same database the change ran on.
type Sealer interface {
	RetireStaleSeals(ctx context.Context, current string) (int64, error)
	InsertProvisionedPair(ctx context.Context, pair ProvisionedPair) error
	Querier() datastore.Querier
}

// ProvisionedPair is one generated pair the sealer stores: the sealed
// private half, the publishable public half, and the row's algorithm.
type ProvisionedPair struct {
	KeyID     string
	Algorithm string
	KeyType   string
	PublicKey string
	SealedKey string
	SealFP    string
}

// PairGenerator mints one fresh signing pair. It is satisfied by the
// seeders package's builder in production; the interface keeps the service
// from importing the seeders (which import this package).
type PairGenerator func(algorithm string) (ProvisionedPair, error)

// GeneratePair mints one fresh signing pair sealed with the service's
// cipher. It is the production PairGenerator: the row shape is this
// package's decision, so the generation lives beside the schema, and the
// seeders and the jwks:generate command build on the same function.
func (s *Service) GeneratePair(algorithm string) (ProvisionedPair, error) {
	return generatePair(algorithm, s.cipher)
}

// GeneratePairWith is the free-standing builder the seeders package and the
// jwks:generate command call. It is the same generation the Service's
// GeneratePair method runs, without needing a Service.
func GeneratePairWith(algorithm string, cipher *crypto.Cipher) (ProvisionedPair, error) {
	return generatePair(algorithm, cipher)
}

// generatePair is the shared builder. It lives free-standing so the
// seeders package can call it without a Service.
func generatePair(algorithm string, cipher *crypto.Cipher) (ProvisionedPair, error) {
	private, public, err := crypto.GenerateKeyPair(algorithm)
	if err != nil {
		return ProvisionedPair{}, fmt.Errorf("generate key pair: %w", err)
	}

	// The columns carry the JWK JSON documents, not the base64 envelope the
	// generator writes: the published set and the unseal path both parse the
	// column value directly. Both halves are re-encoded here, so the row and
	// the reader agree on the shape.
	privateParsed, err := crypto.DecodeJWK(private)
	if err != nil {
		return ProvisionedPair{}, fmt.Errorf("read private key: %w", err)
	}
	publicParsed, err := crypto.DecodeJWK(public)
	if err != nil {
		return ProvisionedPair{}, fmt.Errorf("read public key: %w", err)
	}
	privateDoc, err := json.Marshal(privateParsed)
	if err != nil {
		return ProvisionedPair{}, fmt.Errorf("encode private key: %w", err)
	}
	publicDoc, err := json.Marshal(publicParsed)
	if err != nil {
		return ProvisionedPair{}, fmt.Errorf("encode public key: %w", err)
	}

	// The public half's kid and alg are what the row stores; the same stamp
	// rides the private half, so the pair answers one name.
	kid, _ := publicParsed.KeyID()
	keyType := publicParsed.KeyType().String()

	sealed, err := cipher.Encrypt(string(privateDoc))
	if err != nil {
		return ProvisionedPair{}, fmt.Errorf("seal private key: %w", err)
	}
	return ProvisionedPair{
		KeyID:     kid,
		Algorithm: algorithm,
		KeyType:   keyType,
		PublicKey: string(publicDoc),
		SealedKey: sealed,
		SealFP:    cipher.Fingerprint(),
	}, nil
}

// Service owns the published key set and the configured signing material.
type Service struct {
	source Source
	log    *slog.Logger

	// once guards the parse of the configured HMAC secret: it is read from
	// a string that does not change for the life of the process, and a
	// parse failure is remembered rather than retried on every request.
	once sync.Once

	// hmacKey is the symmetric half, from auth.secret_key. It signs and
	// verifies locally and is never published.
	hmacKey jwk.Key

	// cipher unseals a stored signing pair's private key. A run without a
	// secret key has nothing to unseal; signing fails closed on it.
	cipher *crypto.Cipher

	// sealer is the optional write seam: when wired, a row whose seal
	// fingerprint no longer matches the cipher's is retired in place and a
	// replacement pair is provisioned, so an AUTH_SECRET_KEY rotation
	// recovers on the next sign-in instead of staying broken until an
	// operator runs a command.
	sealer    Sealer
	generate  PairGenerator
	algorithm string
	recorder  *audit.Recorder

	// configured is auth.jwt_algorithm, empty when the deployment lets the
	// material decide.
	configured string

	parseErr error
}

// WithSealer wires the auto-invalidation write path. generate mints a
// fresh pair (the seeders builder in production) and algorithm names the
// algorithm the replacement is generated with — the configured
// auth.jwt_algorithm's asymmetric answer, or the package default. Without
// a sealer, a stale-fingerprint row fails signing with its decrypt error
// and the operator recovers with jwks:generate. recorder may be nil — a
// record is best effort, and an invalidation that cannot be recorded has
// still restored signing.
func (s *Service) WithSealer(sealer Sealer, generate PairGenerator, algorithm string, recorder *audit.Recorder) *Service {
	s.sealer = sealer
	s.generate = generate
	s.algorithm = algorithm
	s.recorder = recorder
	return s
}

// NewService builds the service. source may be nil, which is a run with no
// signing table to read: signing then fails closed until a source is wired.
// cipher may be nil — a run without a secret key cannot unseal a stored
// private key, and signing fails closed on it.
func NewService(cfg config.Config, source Source, cipher *crypto.Cipher, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	service := &Service{source: source, cipher: cipher, log: log}
	// The parse runs here rather than on the first request: a secret that
	// cannot be read is a broken deployment, and it should be reported
	// before the listener opens instead of as a 500 on a client's first
	// verification.
	service.parse(cfg)
	return service
}

// parse reads the configured HMAC secret once. A missing secret is not an
// error — the asymmetric stack signs from the database — but a value that
// is present and unreadable is.
func (s *Service) parse(cfg config.Config) {
	s.once.Do(func() {
		s.configured = cfg.Auth.JWTAlgorithm
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
	if _, ok := jwa.LookupSignatureAlgorithm(s.configured); !ok {
		return fmt.Errorf("jwks: auth.jwt_algorithm: %q is not a signature algorithm", s.configured)
	}
	// The material rule is the config package's: the same mismatch Validate
	// refuses at start-up is refused here, for a Service built without
	// validating.
	return config.JWTAlgorithmMaterialError(
		s.configured, s.configuredSecretKey())
}

// configuredSecretKey reports the HMAC secret's presence in the form the
// shared material rule reads.
func (s *Service) configuredSecretKey() string {
	if s.hmacKey != nil {
		return "configured"
	}
	return ""
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

// SigningAlgorithm reports the algorithm a token is signed with.
//
// The configured auth.jwt_algorithm wins when it is set: that is the value a
// deployment gives when it configures both stacks, and the only way it can
// say which one signs. When it is unset the answer is derived from the
// material — the algorithm the active signing row carries when the database
// holds one, the HMAC secret's length otherwise — so a deployment with one
// stack never has to state what its material already says.
func (s *Service) SigningAlgorithm() (jwa.SignatureAlgorithm, error) {
	if s.parseErr != nil {
		return jwa.NoSignature(), s.parseErr
	}
	if s.configured != "" {
		alg, ok := jwa.LookupSignatureAlgorithm(s.configured)
		if !ok {
			return jwa.NoSignature(), fmt.Errorf("jwks: auth.jwt_algorithm: %q is not a signature algorithm", s.configured)
		}
		// The named algorithm must match the material. Configuration
		// validation refuses the mismatch at start-up, so reaching here is a
		// caller that built a Service without validating; the shared rule
		// answers the same way either path built it.
		if err := config.JWTAlgorithmMaterialError(
			s.configured, s.configuredSecretKey()); err != nil {
			return jwa.NoSignature(), err
		}
		return alg, nil
	}

	// The asymmetric half signs from the database: an active signing row
	// carries the algorithm in its own column, so the row decides when the
	// deployment names no override.
	if pairs, err := s.signingPairs(context.Background()); err == nil && len(pairs) > 0 {
		row := pairs[0]
		if row.Algorithm == "" {
			// The generator always stamps `alg`, so a row without one was
			// written by hand. The curve determines the algorithm, but the
			// mapping lives in an internal jwx package, so rather than
			// restating it here the deployment is asked to say what it means.
			return jwa.NoSignature(), fmt.Errorf(
				"jwks: signing key %s carries no algorithm; set auth.jwt_algorithm", row.KeyID)
		}
		looked, ok := jwa.LookupSignatureAlgorithm(row.Algorithm)
		if !ok {
			return jwa.NoSignature(), fmt.Errorf(
				"jwks: signing key %s: %q is not a signature algorithm", row.KeyID, row.Algorithm)
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

// signingPairs reads the database's active signing key pairs. A nil or
// non-PairSource source answers nothing: a run with no table wired cannot
// sign asymmetrically.
func (s *Service) signingPairs(ctx context.Context) ([]SigningKeyPair, error) {
	pairs, ok := s.source.(PairSource)
	if !ok || pairs == nil {
		return nil, ErrNoStoredKeys
	}
	return pairs.ActiveSigningKeyPairs(ctx)
}

// SignKey returns the active signing key pair's private key, the default for
// stateless JWTs. It is the key a client verifies against the published set,
// so it is what a token meant for an outside caller is signed with.
func (s *Service) SignKey(ctx context.Context) (jwk.Key, error) {
	if s.parseErr != nil {
		return nil, s.parseErr
	}
	return s.signingPrivateKey(ctx)
}

// signingPrivateKey unseals the first active signing row's private key. The
// rows are ordered stably, so the first row is the key every mint names.
// An AUTH_SECRET_KEY rotation discovered on the way re-provisions the rows,
// so the read runs once more before the failure is answered.
func (s *Service) signingPrivateKey(ctx context.Context) (jwk.Key, error) {
	pairs, err := s.signingPairs(ctx)
	if err != nil {
		return nil, err
	}
	if len(pairs) == 0 {
		return nil, ErrNoStoredKeys
	}
	opened, err := s.openSealedKey(ctx, pairs[0])
	if errors.Is(err, errRetryAfterInvalidation) {
		// The rotation re-read: the fresh rows carry the current fingerprint,
		// so this open cannot trip the retry again.
		var retryErr error
		pairs, retryErr = s.signingPairs(ctx)
		if retryErr != nil {
			return nil, retryErr
		}
		if len(pairs) == 0 {
			return nil, ErrNoStoredKeys
		}
		opened, err = s.openSealedKey(ctx, pairs[0])
	}
	if err != nil {
		return nil, err
	}
	// A token's header names the row's kid, and the published set matches
	// it by that name, so the unsealed key adopts the row's kid — the JWK
	// JSON a rotation wrote earlier is not the authority, the row is.
	stamped, err := s.stampSigningKey(opened, pairs[0])
	if err != nil {
		return nil, err
	}
	return stamped, nil
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

// VerifyKeySet returns the public keys a token may be verified against:
// every active signing key the database holds.
//
// The HMAC secret is deliberately absent. A JWKS is a public document, and a
// symmetric key's "public" form is the secret itself, so publishing it would
// hand every reader the ability to mint tokens. A caller that must verify an
// HS* token takes HMACKey instead.
//
// A database that cannot be read fails the call rather than answering a
// partial set: the set is the source of truth for what verifies, and a
// reader that misses a row it should have would refuse a valid token — the
// failure must be visible, not silent.
func (s *Service) VerifyKeySet(ctx context.Context) (jwk.Set, error) {
	if s.parseErr != nil {
		return nil, s.parseErr
	}

	set := jwk.NewSet()
	seen := make(map[string]struct{}, 1)

	stored, err := s.storedKeys(ctx)
	if err != nil {
		return nil, err
	}

	for _, key := range stored {
		// A row carrying the same `kid` as one already added does not appear
		// twice: a set with one key named twice is a set a client cannot
		// index.
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
		opened, openErr := s.openSealedKey(ctx, row)
		if errors.Is(openErr, errRetryAfterInvalidation) {
			// The rotation retired this row mid-set; the re-read names the
			// replacement, and its rows carry the current fingerprint.
			var readErr error
			rows, readErr = pairs.ActiveSigningKeyPairs(ctx)
			if readErr != nil {
				return nil, readErr
			}
			if len(rows) == 0 {
				return nil, ErrNoStoredKeys
			}
			keys = keys[:0]
			for _, fresh := range rows {
				retryOpened, retryErr := s.openSealedKey(ctx, fresh)
				if retryErr != nil {
					s.log.ErrorContext(ctx, "jwks: skipping an unusable signing pair",
						"kid", fresh.KeyID, "err", retryErr)
					continue
				}
				key, stampErr := s.stampSigningKey(retryOpened, fresh)
				if stampErr != nil {
					return nil, stampErr
				}
				keys = append(keys, key)
			}
			break
		}
		if openErr != nil {
			s.log.ErrorContext(ctx, "jwks: skipping an unusable signing pair",
				"kid", row.KeyID, "err", openErr)
			continue
		}
		key, err := s.stampSigningKey(opened, row)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, ErrNoStoredKeys
	}
	return keys, nil
}

// stampSigningKey names an unsealed private key after its row: the kid the
// token header carries and the algorithm the row declares.
func (s *Service) stampSigningKey(opened jwk.Key, row SigningKeyPair) (jwk.Key, error) {
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
	return opened, nil
}

// openSealedKey unseals one row's private key and parses it. A symmetric
// result is refused: a shared secret has no place in a published set.
//
// A row whose seal fingerprint differs from the cipher's was sealed by an
// AUTH_SECRET_KEY the process no longer holds — its ciphertext can never
// open again. When the sealer is wired the rotation runs here once: the
// stale rows are retired in place and a replacement pair is provisioned,
// and the caller retries against the new first row. Without a sealer the
// mismatch is the returned error, and jwks:generate is the operator's way
// out.
func (s *Service) openSealedKey(ctx context.Context, row SigningKeyPair) (jwk.Key, error) {
	if s.cipher == nil {
		return nil, errors.New("no auth secret is configured to unseal the private key")
	}
	if row.SealFP != "" && row.SealFP != s.cipher.Fingerprint() {
		invalidated, err := s.invalidateStaleSeals(ctx)
		if err != nil {
			return nil, err
		}
		if invalidated {
			// The stale rows are gone and a fresh pair rests as the first
			// row; the caller's cached slice no longer names it.
			return nil, errRetryAfterInvalidation
		}
		return nil, fmt.Errorf(
			"unseal private key: sealed by a previous AUTH_SECRET_KEY (seal_fp %s…) — run saka jwks:generate",
			row.SealFP[:min(8, len(row.SealFP))])
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

// errRetryAfterInvalidation is the internal signal the unseal path answers
// after it retired stale rows and provisioned a replacement: the caller
// re-reads the rows once and signs with the new first row. It never
// escapes the service.
var errRetryAfterInvalidation = errors.New("jwks: signing rows were re-provisioned")

// invalidateStaleSeals retires every row the current seal fingerprint does
// not name and provisions one replacement pair. It answers whether the
// rotation ran. The write is idempotent by construction: a second run
// finds no stale row (the retire is the WHERE clause) and does nothing.
// The run is recorded on the repository's own query surface — the retire
// and the insert are separate statements here, not one transaction, so a
// crash between them leaves the retired rows sealed-by-nothing (already
// unopenable) and the next run provisions.
func (s *Service) invalidateStaleSeals(ctx context.Context) (bool, error) {
	if s.sealer == nil || s.generate == nil {
		return false, nil
	}
	retired, err := s.sealer.RetireStaleSeals(ctx, s.cipher.Fingerprint())
	if err != nil {
		return false, fmt.Errorf("jwks: retire stale seals: %w", err)
	}
	pair, err := s.generate(s.algorithm)
	if err != nil {
		return false, fmt.Errorf("jwks: generate replacement pair: %w", err)
	}
	if err := s.sealer.InsertProvisionedPair(ctx, pair); err != nil {
		return false, fmt.Errorf("jwks: store replacement pair: %w", err)
	}
	s.log.WarnContext(ctx, "jwks: AUTH_SECRET_KEY rotated — stale signing keys retired and a replacement provisioned",
		"kid", pair.KeyID, "algorithm", pair.Algorithm, "retired", retired)
	if s.recorder != nil {
		q := s.sealer.Querier()
		s.recorder.Record(ctx, q, audit.Entry{
			Event:  audit.EventJwksInvalidated,
			Status: audit.StatusSuccess,
			Payload: map[string]string{
				"kid":       pair.KeyID,
				"algorithm": pair.Algorithm,
				"retired":   strconv.FormatInt(retired, 10),
			},
		})
	}
	return true, nil
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
