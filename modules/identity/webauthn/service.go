package webauthn

import (
	"context"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"strings"
	"time"

	"uuid"

	"github.com/go-webauthn/webauthn/protocol"
	gowebauthn "github.com/go-webauthn/webauthn/webauthn"
	"github.com/huandu/go-sqlbuilder"
	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/signin"
)

// Ceremony session and step-up token windows. The ceremony window is how
// long a challenge may sit unanswered — a browser prompts within seconds;
// a minute is generous. The reauthentication window mirrors the pending
// MFA bridge: a proof a holder confirmed seconds ago is spent within
// minutes or not at all.
const (
	ceremonyTTL = time.Minute
	reauthTTL   = 5 * time.Minute
)

// Setting keys the service reads through the settings reader. The catalog
// owns the defaults; these names are the contract between the two.
const (
	SettingAllowSyncedPasskeys = "webauthn.allow_synced_passkeys"
	SettingUserVerification    = "webauthn.user_verification"
	SettingMaxCredentials      = "passkey.max_credentials"
	SettingMaxEnrollments      = "mfa.max_enrollments"
)

// Settings defaults the service falls back to when the catalog does not
// carry the key yet — the state of a deployment mid-upgrade — and the values
// validation keeps the catalog honest about.
const (
	defaultAllowSyncedPasskeys = true
	defaultUserVerification    = UserVerificationRequired
	defaultMaxCredentials      = 10
	defaultMaxEnrollments      = 10
	maxSettingLimit            = 50
)

// Errors the ceremonies and the management surface answer with. The handler
// maps them to connect codes; the service only classifies.
var (
	ErrCeremonyInvalid    = errors.New("webauthn: ceremony session is unknown, spent, or expired")
	ErrAssertionInvalid   = errors.New("webauthn: assertion or attestation failed verification")
	ErrVerificationDue    = errors.New("webauthn: user verification is required for this ceremony")
	ErrSyncedPasskeyOff   = errors.New("webauthn: synced passkeys are not allowed by this deployment")
	ErrTooManyPasskeys    = errors.New("webauthn: the account holds the maximum number of passkeys")
	ErrTooManyEnrollments = errors.New("webauthn: the account holds the maximum number of second factors")
	ErrCredentialForeign  = errors.New("webauthn: the credential names another account")
	ErrLastWayIn          = errors.New("webauthn: the removal would leave the account no way in")
	ErrClonedCredential   = errors.New("webauthn: the credential shows signs of cloning")
	ErrSettingUnreadable  = errors.New("webauthn: the passkey settings are unreadable")
	ErrAccountUnknown     = errors.New("webauthn: the account is not found")
)

// SettingsReader is the settings surface the service reads its knobs
// through. *appconfig.Settings satisfies it; the interface keeps this
// package from importing the appconfig feature back.
type SettingsReader interface {
	GetString(ctx context.Context, key string) (string, error)
	GetBool(ctx context.Context, key string) (bool, error)
	GetInt64(ctx context.Context, key string) (int64, error)
}

// TotpEnrollmentCounter is the seam to the second factor's own enrollments:
// mfa.max_enrollments counts TOTP devices and passkeys together, and the
// count of the TOTP half lives in the multifactor package. Wired after
// construction; nil means the account carries nothing the seam can count,
// which is the area-test state.
type TotpEnrollmentCounter interface {
	CountConfirmedTotp(ctx context.Context, db datastore.Querier, userID uuid.UUID) (int, error)
}

// issuer is the sign-in seam: the account lookup the assertion resolves
// with, and the session opening a proven identity buys. *signin.Service
// satisfies it; the interface keeps this package from importing the
// multifactor feature the way the gate's wiring avoids the cycle.
type issuer interface {
	FindAccountByID(ctx context.Context, id uuid.UUID) (*signin.Account, error)
	// FindAccountByIDAny answers the account whether or not a password row
	// rides it — the passkey surfaces' resolution, whose holders sign in by
	// credential alone. The stranding check keeps the inner-join read: its
	// question is "is there a way back in", not "is there an account".
	FindAccountByIDAny(ctx context.Context, id uuid.UUID) (*signin.Account, error)
	// VerifyPassword checks a password against the named account's stored
	// hash — the re-proof the step-up surface runs.
	VerifyPassword(ctx context.Context, id uuid.UUID, password string) (bool, error)
	IssueSession(ctx context.Context, db datastore.Querier, account *signin.Account, provider, event string, params signin.SessionParams) (signin.Result, error)
}

// Service carries the passkey ceremonies. The go-webauthn engine is built
// once at construction from the deployment's public origin — the RP identity
// credentials bind to — and every ceremony reads its knobs through the
// settings reader at call time, so an operator's change lands without a
// restart.
type Service struct {
	pool     *datastore.Postgres
	repo     *Repository
	issuer   issuer
	settings SettingsReader
	engine   *gowebauthn.WebAuthn
	audit    *audit.Recorder
	log      *slog.Logger
	now      func() time.Time

	// totpEnrollments is the post-construction seam to the second factor's
	// own enrollment count. Nil until the area wires it; a nil counter
	// counts zero TOTP devices.
	totpEnrollments TotpEnrollmentCounter
}

// NewService builds the service over the deployment's pool and settings.
//
// The RP identity derives from app.base_url: the RP ID is its hostname and
// the origin is the base URL itself. This is load-bearing — credentials are
// bound to the RP ID, so changing the deployment origin silently orphans
// every passkey ever enrolled. The docs must say so.
func NewService(cfg config.Config, pool *datastore.Postgres, repo *Repository, issuer issuer, settings SettingsReader, recorder *audit.Recorder, log *slog.Logger) (*Service, error) {
	origin := strings.TrimSuffix(cfg.App.BaseURL, "/")
	host, err := rpIDFrom(origin)
	if err != nil {
		return nil, fmt.Errorf("webauthn: %w", err)
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	engine, err := gowebauthn.New(&gowebauthn.Config{
		RPID:          host,
		RPDisplayName: "Tango",
		RPOrigins:     []string{origin},
		Timeouts: gowebauthn.TimeoutsConfig{
			Login:        gowebauthn.TimeoutConfig{Timeout: ceremonyTTL, Enforce: true},
			Registration: gowebauthn.TimeoutConfig{Timeout: ceremonyTTL, Enforce: true},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("webauthn: engine: %w", err)
	}
	return &Service{
		pool:     pool,
		repo:     repo,
		issuer:   issuer,
		settings: settings,
		engine:   engine,
		audit:    recorder,
		log:      log,
		now:      time.Now,
	}, nil
}

// WithTotpEnrollments wires the second factor's enrollment counter after
// construction — the consuming-package seam that keeps the multifactor
// package out of this one's import graph.
func (s *Service) WithTotpEnrollments(counter TotpEnrollmentCounter) *Service {
	s.totpEnrollments = counter
	return s
}

// rpIDFrom derives the RP ID from an origin: the hostname, without scheme,
// port, or path. A base URL without a host is a misconfiguration the
// construction refuses — credentials bound to a guessed ID would be worthless.
func rpIDFrom(origin string) (string, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("app.base_url carries no host for the RP ID")
	}
	return parsed.Hostname(), nil
}

// userVerification reads the ceremony's user-verification level. An
// unreadable setting fails closed — the ceremony is refused, not waved
// through — because the level is what makes an assertion satisfy an MFA
// bridge. A value the table's check constraint would refuse is refused the
// same way.
func (s *Service) userVerification(ctx context.Context) (string, error) {
	if s.settings == nil {
		return "", ErrSettingUnreadable
	}
	value, err := s.settings.GetString(ctx, SettingUserVerification)
	if err != nil {
		if errors.Is(err, errUnknownSetting) {
			return defaultUserVerification, nil
		}
		return "", fmt.Errorf("%w: %v", ErrSettingUnreadable, err)
	}
	switch value {
	case UserVerificationRequired, UserVerificationPreferred, UserVerificationDiscouraged:
		return value, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrSettingUnreadable, value)
	}
}

// allowSyncedPasskeys reads the synced-passkey toggle. Unreadable fails
// closed; unknown falls back to the catalog's default, the same state the
// toggle shipped as.
func (s *Service) allowSyncedPasskeys(ctx context.Context) (bool, error) {
	if s.settings == nil {
		return false, ErrSettingUnreadable
	}
	value, err := s.settings.GetBool(ctx, SettingAllowSyncedPasskeys)
	if err != nil {
		if errors.Is(err, errUnknownSetting) {
			return defaultAllowSyncedPasskeys, nil
		}
		return false, fmt.Errorf("%w: %v", ErrSettingUnreadable, err)
	}
	return value, nil
}

// limitInt reads one integer limit. The bounds match the catalog's Validate:
// 1..maxSettingLimit. Unreadable fails closed; unknown falls back to the
// catalog's default.
func (s *Service) limitInt(ctx context.Context, key string, fallback int) (int, error) {
	if s.settings == nil {
		return 0, ErrSettingUnreadable
	}
	value, err := s.settings.GetInt64(ctx, key)
	if err != nil {
		if errors.Is(err, errUnknownSetting) {
			return fallback, nil
		}
		return 0, fmt.Errorf("%w: %v", ErrSettingUnreadable, err)
	}
	if value < 1 || value > maxSettingLimit {
		return 0, fmt.Errorf("%w: %s=%d", ErrSettingUnreadable, key, value)
	}
	return int(value), nil
}

// errUnknownSetting is the sentinel the settings reader answers for a key the
// catalog does not carry. It is matched by string identity because the
// appconfig package cannot sit below this one without a cycle the
// settings-reader interface exists to avoid.
var errUnknownSetting = errors.New("unknown setting")

// lockAccount serializes the account's mutating ceremonies on one advisory
// lock inside the caller's transaction: the enrollment limits and the
// stranding check are count-then-write judgements, and two racers holding
// the same account's lock read each other's committed state. The key is the
// account's own — deployments serialize per holder, never globally.
func lockAccount(ctx context.Context, db datastore.Querier, userID uuid.UUID) error {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("pg_advisory_xact_lock(hashtextextended(" + sb.Var(userID.String()) + ", 0))")
	query, args := sb.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("webauthn: account lock: %w", err)
	}
	return nil
}

// credentialUser is the adapter the engine sees: the account's identity as
// the WebAuthn user handle (the UUID's bytes — stable, unique, and what the
// usernameless assertion's userHandle decodes to), and the credentials the
// account holds.
type credentialUser struct {
	id          uuid.UUID
	name        string
	displayName string
	credentials []gowebauthn.Credential
}

func (u *credentialUser) WebAuthnID() []byte                           { return u.id[:] }
func (u *credentialUser) WebAuthnName() string                         { return u.name }
func (u *credentialUser) WebAuthnDisplayName() string                  { return u.displayName }
func (u *credentialUser) WebAuthnCredentials() []gowebauthn.Credential { return u.credentials }

// toEngineCredential maps one stored row into the engine's credential shape —
// the form ValidateLogin compares an assertion against, counter included.
func toEngineCredential(row CredentialSchema) (gowebauthn.Credential, error) {
	transport, err := decodeTransports(row.Transport)
	if err != nil {
		return gowebauthn.Credential{}, fmt.Errorf("webauthn: transports: %w", err)
	}
	aaguid := [16]byte{}
	if row.AAGUID != nil && *row.AAGUID != "" {
		clean := strings.ReplaceAll(strings.ToLower(*row.AAGUID), "-", "")
		raw, err := hex.DecodeString(clean)
		if err != nil || len(raw) != 16 {
			return gowebauthn.Credential{}, fmt.Errorf("webauthn: aaguid %q is not a UUID", *row.AAGUID)
		}
		copy(aaguid[:], raw)
	}
	return gowebauthn.Credential{
		ID:              row.CredentialID,
		PublicKey:       row.PublicKey,
		AttestationType: row.AttestationType,
		Transport:       transport,
		Flags: gowebauthn.CredentialFlags{
			BackupEligible: row.BackupEligible,
			BackupState:    row.BackupState,
		},
		Authenticator: gowebauthn.Authenticator{
			AAGUID:       aaguid[:],
			SignCount:    engineCounter(row.SignCount),
			CloneWarning: false,
		},
	}, nil
}

// engineCounter clamps the stored signature counter into the uint32 the
// protocol defines. The column is a BIGINT so a corrupted or hostile value
// cannot overflow the engine's comparison; the protocol never exceeds 32
// bits.
func engineCounter(count int64) uint32 {
	if count < 0 {
		return 0
	}
	if count > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(count)
}

// decodeTransports reads the JSONB transport array back into the engine's
// slice. An empty column decodes to an empty slice — a real state, not an
// error.
func decodeTransports(raw []byte) ([]protocol.AuthenticatorTransport, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "[]" || trimmed == "null" {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, err
	}
	out := make([]protocol.AuthenticatorTransport, 0, len(names))
	for _, name := range names {
		out = append(out, protocol.AuthenticatorTransport(name))
	}
	return out, nil
}

// formatAAGUID renders the engine's AAGUID in its canonical dashed
// lowercase form — the form the column's check constraint admits and the
// settings page displays.
func formatAAGUID(raw []byte) string {
	padded := make([]byte, 16)
	copy(padded, raw)
	hexed := hex.EncodeToString(padded)
	return strings.Join([]string{hexed[0:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:32]}, "-")
}

// view maps one stored row into the wire's credential view.
func view(row CredentialSchema) (View, error) {
	transports, err := decodeTransports(row.Transport)
	if err != nil {
		return View{}, err
	}
	names := make([]string, 0, len(transports))
	for _, transport := range transports {
		names = append(names, string(transport))
	}
	wireID, err := IDFromUUID(row.ID)
	if err != nil {
		return View{}, fmt.Errorf("webauthn: credential id: %w", err)
	}
	return View{
		ID:             wireID.String(),
		Name:           row.Name,
		AAGUID:         derefOrEmpty(row.AAGUID),
		BackupEligible: row.BackupEligible,
		BackupState:    row.BackupState,
		Transports:     names,
		SignCount:      row.SignCount,
		CreatedAt:      row.CreatedAt,
		LastUsedAt:     row.LastUsedAt,
	}, nil
}

func derefOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// View is the credential's wire form: what a settings page renders and what
// the enrollment and rename procedures answer. It carries no key material.
type View struct {
	ID             string
	Name           string
	AAGUID         string
	BackupEligible bool
	BackupState    bool
	Transports     []string
	SignCount      int64
	CreatedAt      time.Time
	LastUsedAt     *time.Time
}

// IssuedSession is the sign-in outcome a verified assertion buys — the
// sign-in service's own Result, verbatim, so a client treats the two alike.
type IssuedSession = signin.Result
