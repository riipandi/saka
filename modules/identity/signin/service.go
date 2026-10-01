package signin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"go.jetify.com/typeid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/jwks"
	"github.com/riipandi/tango/modules/identity/password"
	"github.com/riipandi/tango/modules/identity/session"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/jwtutils"
	"uuid"
)

// The failures a sign-in reports. The handler maps them to connect codes, so
// the wire form of a refusal lives with the transport, not here.
var (
	// ErrInvalidCredentials covers both a missing account and a wrong
	// password: answering differently would tell a caller which half was
	// wrong and turn the endpoint into an account enumerator.
	ErrInvalidCredentials = errors.New("signin: invalid credentials")

	// ErrAccountDisabled is an account switched off by an operator.
	ErrAccountDisabled = errors.New("signin: account is disabled")

	// ErrAccountBanned is an account inside its ban window. The reason is
	// not attached: it is operator-facing material, not a wire detail.
	ErrAccountBanned = errors.New("signin: account is banned")

	// ErrEmailUnverified is the account whose email address no verification
	// code has confirmed yet. It is its own answer — distinct from the
	// invalid-credentials one — so the client can route the account to the
	// verification screen without a second probe.
	ErrEmailUnverified = errors.New("signin: email address is not verified")
)

// TokenType is the authorization scheme the access token is presented under.
const TokenType = jwtutils.BearerScheme

// Service verifies the primary credential and issues the token pair.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// mfa is the second factor's gate, wired after both services exist: the
	// sign-in asks it whether the verified account keeps a confirmed
	// authenticator and mints the pending bridge through it. A nil gate is
	// the state a unit test is in, and answers "no second factor" — the
	// sign-in is one factor, exactly as it was before the feature existed.
	mfa mfaGate
	// audit writes the record of a successful sign-in. It is the shared
	// recorder, so the record's columns and vocabulary are decided in one
	// place rather than here.
	audit     *audit.Recorder
	keys      *jwks.Service
	hasher    *crypto.PasswordHasher
	log       *slog.Logger
	issuer    string
	accessTTL time.Duration
	now       func() time.Time

	// settings reads the session bound the database owns. Nil keeps the
	// catalog default — the state a bare wiring is in.
	settings sessionSettings

	// policy is the breach check's runtime source. Nil arms no flag — the
	// state a bare wiring is in.
	policy *password.Validator

	// notices is the new-device notification channel. Nil until wired; a
	// service without one skips the mail, never the sign-in.
	notices deviceNoticeEnqueuer

	// dummyHash holds the hash a sign-in of an unknown account is checked
	// against, so the two failure paths cost the same work. It is computed
	// once, on the first miss.
	dummyHash once[string]
}

// mfaGate is the second factor's sign-in seam: the gate question the password
// success asks and the bridge it mints. The multifactor service satisfies it;
// the interface keeps the sign-in package from importing it back.
type mfaGate interface {
	// GateSignIn answers the challenge a verified password hands over: the
	// pending token, its expiry, and whether a challenge is owed at all.
	GateSignIn(ctx context.Context, userID uuid.UUID, remember bool) (PendingSignIn, error)
	// GateEnrollment mints the bridge the `mfa.required` gate hands over:
	// the password is proven, the account keeps no confirmed factor, and
	// the enrollment endpoints are what the bridge admits.
	GateEnrollment(ctx context.Context, userID uuid.UUID, remember bool) (PendingSignIn, error)
	// KeepsConfirmedFactor answers whether the account holds a confirmed
	// authenticator — the question the sign-in's fork runs on.
	KeepsConfirmedFactor(ctx context.Context, userID uuid.UUID) (bool, error)
}

// PendingSignIn is the bridge the gate mints. It is the sign-in package's
// own view of the multifactor outcome, so the two packages share no type
// beyond this shape.
type PendingSignIn struct {
	Token     string
	ExpiresAt time.Time
}

// NewService builds the service. keys is the area's key-set service: the
// signing material resolves through it, so the dual stack (key pair or HMAC
// secret) is the deployment's decision, not this feature's.
func NewService(cfg config.Config, pool *datastore.Postgres, repo *Repository, keys *jwks.Service, recorder *audit.Recorder, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		pool:      pool,
		repo:      repo,
		audit:     recorder,
		keys:      keys,
		hasher:    crypto.NewPasswordHasher(),
		log:       log,
		issuer:    cfg.Auth.Issuer,
		accessTTL: cfg.Auth.AccessTTL,
		now:       time.Now,
	}
}

// Params carries one sign-in attempt. IPAddress is the caller's address as
// the transport read it, empty when it is unknown.
type Params struct {
	Identity  string
	Password  string
	Remember  bool
	UserAgent string
	IPAddress string
	// Fingerprint is the browser fingerprint the transport read from the
	// request header. It is opaque: it is stored beside the session and the
	// audit record, never interpreted.
	Fingerprint string
}

// User is the account view a successful sign-in answers with.
type User struct {
	ID          string
	Username    string
	Email       string
	DisplayName string
}

// Result is the token pair and the account it was issued for.
type Result struct {
	AccessToken      string
	TokenType        string
	AccessExpiresIn  int32
	RefreshExpiresIn int32
	RefreshToken     string
	SessionID        string
	User             User

	// MFARequired marks the fork: true, the account keeps a confirmed
	// authenticator and the token fields are empty — the caller completes
	// the sign-in through the second factor with the pending token.
	MFARequired bool
	// MFAEnrollmentRequired marks the `mfa.required` fork: the account
	// kept no confirmed factor, so the pending token admits the
	// enrollment endpoints instead of the challenge — no token until a
	// factor is confirmed, and the sign-in completes through the same
	// CompleteSignIn afterward.
	MFAEnrollmentRequired bool
	// MFAPendingToken is the bridge the password check minted.
	MFAPendingToken string
	// MFAPendingExpiresAt is when the bridge dies.
	MFAPendingExpiresAt time.Time
}

// SignIn verifies the credential and issues the access and refresh tokens.
//
// The refresh token is the only server-side state: a hashed row in the
// sessions table, so a later procedure can rotate and revoke it without the
// access token ever depending on a lookup.
func (s *Service) SignIn(ctx context.Context, params Params) (Result, error) {
	account, err := s.repo.FindAccountByIdentity(ctx, params.Identity)
	if errors.Is(err, datastore.ErrNoRows) {
		// The same cost runs here as on a password mismatch, so the
		// response time does not disclose whether the account exists.
		s.verifyDummy(params.Password)
		return Result{}, ErrInvalidCredentials
	}
	if err != nil {
		return Result{}, err
	}

	match, verifyErr := s.hasher.Verify(params.Password, account.PasswordHash)
	if verifyErr != nil {
		// A stored hash that cannot be read is an account that can never
		// sign in; the answer to the caller is the same as a mismatch, and
		// the log carries the difference.
		s.log.ErrorContext(ctx, "signin: unreadable password hash", "error", verifyErr)
		return Result{}, ErrInvalidCredentials
	}
	if !match {
		return Result{}, ErrInvalidCredentials
	}

	// The verification gate runs before the second factor's fork: an
	// account whose address no code has confirmed yet owes the code, not a
	// bridge, and answers the distinct refusal the client routes to the
	// verification screen. The single-use email-code sign-in is a
	// confirmation of its own — the code went to the address — so the fork
	// below keeps its own path.
	if account.EmailVerifiedAt == nil {
		return Result{}, ErrEmailUnverified
	}

	// The second factor's fork runs before any session is opened: an account
	// keeping a confirmed authenticator answers the pending bridge instead of
	// the pair, and the tokens wait for the code. An account keeping none
	// while `mfa.required` stands answers the enrollment bridge — the same
	// no-token shape, routed to enrollment instead of a challenge. The fork
	// is silent in the response otherwise — a one-factor account sees
	// nothing of it.
	if s.mfa != nil {
		owed, owedErr := s.mfa.KeepsConfirmedFactor(ctx, account.ID)
		if owedErr != nil {
			return Result{}, owedErr
		}
		if owed {
			pending, gateErr := s.mfa.GateSignIn(ctx, account.ID, params.Remember)
			if gateErr != nil {
				return Result{}, gateErr
			}
			return Result{
				MFARequired:         true,
				MFAPendingToken:     pending.Token,
				MFAPendingExpiresAt: pending.ExpiresAt,
			}, nil
		}
		if s.mfaRequired(ctx) {
			pending, gateErr := s.mfa.GateEnrollment(ctx, account.ID, params.Remember)
			if gateErr != nil {
				return Result{}, gateErr
			}
			return Result{
				MFARequired:           true,
				MFAEnrollmentRequired: true,
				MFAPendingToken:       pending.Token,
				MFAPendingExpiresAt:   pending.ExpiresAt,
			}, nil
		}
	}

	// The account's state is the issuer's check: a disabled or banned account
	// is refused there, so every way of opening a session refuses the same.
	// The breach corpus's flag rides beside it: a credential the corpus
	// knows opens the session and the record tells the operator the
	// credential must be rotated — a refusal here would be the oracle the
	// recovery path cannot afford, and the corpus is fail-open anyway.
	flagged := s.policy != nil && s.policy.FlagsSignIn(ctx, params.Password)
	var result Result
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		var issueErr error
		result, issueErr = s.IssueSession(ctx, tx, account, ProviderPassword, audit.EventSignIn, SessionParams{
			UserAgent:   params.UserAgent,
			IPAddress:   params.IPAddress,
			Fingerprint: params.Fingerprint,
			Remember:    params.Remember,
		})
		return issueErr
	})
	if err != nil {
		return Result{}, err
	}
	if flagged {
		s.log.WarnContext(ctx, "signin: a breached password opened the session")
		s.audit.Record(ctx, s.pool, audit.Entry{
			Event:  audit.EventSigninBreachedPassword,
			Status: audit.StatusSuccess,
			UserID: account.ID.String(),
			Payload: map[string]string{
				"provider": ProviderPassword,
			},
		})
	}
	return result, nil
}

// SessionParams carries what a session records about the request that opened
// it. IPAddress is the caller's address as the transport read it, empty when
// it is unknown; Fingerprint is the browser fingerprint the transport read
// from the request header, opaque and stored beside the session and the audit
// record, never interpreted.
type SessionParams struct {
	UserAgent   string
	IPAddress   string
	Fingerprint string
	Remember    bool
}

// IssueSession opens a session for an account the caller has proven by another
// means than the primary credential — a password verified a moment ago, a
// one-time access code. It refuses a switched-off or banned account the way
// any issuer must, writes the session row, the last-login stamp, and the audit
// record, and signs the access token.
//
// db is the query surface the writes run on: a caller holding an open
// transaction passes its tx, so the session and whatever caused it commit
// together — a consumed code and the session it opened are one fact, and a
// rollback returns the code — and a caller holding none passes the pool.
//
// event is the audit event the opening is recorded under — the vocabulary's
// decision, not this method's: a password sign-in and a code exchange describe
// different happenings and name themselves. Only a successful opening is
// recorded, so the log's growth stays tied to accounts that exist rather than
// to requests anyone can send.
func (s *Service) IssueSession(ctx context.Context, db datastore.Querier, account *Account, provider, event string, params SessionParams) (Result, error) {
	now := s.now()
	switch {
	case account.Disabled:
		return Result{}, ErrAccountDisabled
	case bannedAt(account, now):
		return Result{}, ErrAccountBanned
	}

	refresh, err := NewRefreshToken()
	if err != nil {
		return Result{}, fmt.Errorf("signin: refresh token: %w", err)
	}

	sessionID, err := typeid.New[session.SessionID]()
	if err != nil {
		return Result{}, fmt.Errorf("signin: session id: %w", err)
	}
	sessionRow := session.SessionSchema{
		ID:                sessionID,
		UserID:            account.ID,
		Provider:          provider,
		TokenHash:         refresh.Hash,
		UserAgent:         params.UserAgent,
		DeviceFingerprint: params.Fingerprint,
		IPAddress:         addrPtr(params.IPAddress),
		Remember:          params.Remember,
		CreatedAt:         now,
		ExpiresAt:         now.Add(s.sessionLifetime(ctx)),
	}
	repo := s.repo.WithQuerier(db)
	if createErr := repo.CreateSession(ctx, sessionRow); createErr != nil {
		return Result{}, createErr
	}
	if touchErr := repo.TouchLastLogin(ctx, account.ID, now); touchErr != nil {
		return Result{}, touchErr
	}
	s.audit.Record(ctx, db, audit.Entry{
		Event:  event,
		Status: audit.StatusSuccess,
		UserID: account.ID.String(),
		Payload: map[string]string{
			"provider":   provider,
			"session_id": sessionID.String(),
		},
	})

	// The first-seen judgement rides the session's transaction, so a
	// rolled-back sign-in leaves no device row behind. A fingerprint-less
	// client is never a device: it cannot be told apart from any other
	// fingerprint-less client, so noticing it would mail every sign-in.
	newDevice := false
	if params.Fingerprint != "" {
		seen, seenErr := repo.MarkDeviceSeen(ctx, db, account.ID, params.Fingerprint, now)
		if seenErr != nil {
			return Result{}, seenErr
		}
		newDevice = seen
	}

	access, err := s.SignAccessToken(ctx, account, sessionID, now)
	if err != nil {
		return Result{}, err
	}

	// The notice is best-effort and rides outside the transaction: the queue
	// client writes on its own connection, the sign-in has committed by the
	// only paths this method returns through, and a failed enqueue never
	// fails a sign-in that already succeeded.
	if newDevice && s.notices != nil {
		s.notices.EnqueueNewDeviceNotice(ctx, NewDeviceNotice{
			UserID:      account.ID.String(),
			Email:       account.Email,
			IPAddress:   params.IPAddress,
			UserAgent:   params.UserAgent,
			Fingerprint: params.Fingerprint,
			SignedInAt:  now,
		})
	}

	return Result{
		AccessToken:      access,
		TokenType:        TokenType,
		AccessExpiresIn:  int32(s.accessTTL.Seconds()),
		RefreshExpiresIn: int32(s.sessionLifetime(ctx).Seconds()),
		RefreshToken:     refresh.Plain,
		SessionID:        sessionID.String(),
		User: User{
			ID:          user.FormatID(account.ID),
			Username:    account.Username,
			Email:       account.Email,
			DisplayName: account.DisplayName,
		},
	}, nil
}

// bannedAt reports whether the account sits inside its ban window. A ban
// without an expiry never lifts by itself.
func bannedAt(account *Account, at time.Time) bool {
	return account.BannedAt != nil && (account.BanExpires == nil || account.BanExpires.After(at))
}

// WithMFAGate arms the second factor's fork. The gate is wired after both
// services construct — the sign-in cannot import the multifactor package
// without a cycle, and the gate's interface keeps the seam one-shaped.
func (s *Service) WithMFAGate(gate mfaGate) *Service {
	s.mfa = gate
	return s
}

// NewDeviceNotice is what a first sighting of a browser fingerprint sends.
// Location (city, country) is not resolved here: no GeoIP source runs in the
// process, so the template's own fallback names it.
type NewDeviceNotice struct {
	UserID      string
	Email       string
	IPAddress   string
	UserAgent   string
	Fingerprint string
	SignedInAt  time.Time
}

// deviceNoticeEnqueuer is the notification channel the first-seen judgement
// feeds. The interface lives here so the sign-in names no queue; the jobs
// package adapts it, the way the ban and MFA notices travel.
type deviceNoticeEnqueuer interface {
	EnqueueNewDeviceNotice(ctx context.Context, notice NewDeviceNotice)
}

// WithDeviceNotifier arms the new-device notice. Nil-safe: a service without
// a notifier signs in as before and only skips the mail.
func (s *Service) WithDeviceNotifier(notices deviceNoticeEnqueuer) *Service {
	s.notices = notices
	return s
}

// AccessTokenTTL is the lifetime the access token is signed with, so a
// renewal answers the same expires_in the sign-in does.
func (s *Service) AccessTokenTTL() time.Duration {
	return s.accessTTL
}

// FindAccountByID exposes the account read the MFA bridge's completion runs:
// the pending row has already named the account, and the session issuer needs
// the row back to open the session. The read runs outside the bridge's
// transaction deliberately: the row was read before the proof, and re-reading
// inside the write transaction would only widen its lock window.
func (s *Service) FindAccountByID(ctx context.Context, id uuid.UUID) (*Account, error) {
	return s.repo.FindAccountByID(ctx, id)
}

// FindAccountByIDAny answers the account whether or not a password row rides
// it — the read the passkey surfaces resolve accounts with, whose holders
// sign in by credential alone.
func (s *Service) FindAccountByIDAny(ctx context.Context, id uuid.UUID) (*Account, error) {
	return s.repo.FindAccountByIDAny(ctx, id)
}

// VerifyPassword checks a password against the account's stored hash — the
// re-proof the step-up surface runs on the caller's own account. The answer
// is a plain boolean: the caller already holds the account, so the refusal
// says only that the proof failed, the way SignIn's pair does without
// naming which half lied.
func (s *Service) VerifyPassword(ctx context.Context, id uuid.UUID, password string) (bool, error) {
	account, err := s.repo.FindAccountByID(ctx, id)
	if err != nil {
		return false, err
	}
	match, verifyErr := s.hasher.Verify(password, account.PasswordHash)
	if verifyErr != nil {
		return false, fmt.Errorf("signin: verify password: %w", verifyErr)
	}
	return match, nil
}

// Challenge mints the pending bridge an account keeping a confirmed
// authenticator owes before its session opens. It is the fork SignIn's
// password success runs, exposed so every path into a session asks the same
// question: a one-time code is possession of the mailbox, and an account
// that keeps a second factor answers the challenge, not the pair. ok is
// false when nothing is owed and no bridge was minted.
func (s *Service) Challenge(ctx context.Context, userID uuid.UUID, remember bool) (PendingSignIn, bool, error) {
	if s.mfa == nil {
		return PendingSignIn{}, false, nil
	}
	owed, err := s.mfa.KeepsConfirmedFactor(ctx, userID)
	if err != nil {
		return PendingSignIn{}, false, err
	}
	if !owed {
		return PendingSignIn{}, false, nil
	}
	pending, err := s.mfa.GateSignIn(ctx, userID, remember)
	if err != nil {
		return PendingSignIn{}, false, err
	}
	return PendingSignIn{Token: pending.Token, ExpiresAt: pending.ExpiresAt}, true, nil
}

// SessionLifetime picks the session lifetime: the remembered and the
// non-remembered path share one bound, `session.max_lifetime` — the
// remembered ceiling a deployment sets on every session the store may hold.
// The read rides the request context, so an operator's change lands at the
// next mint; an unreadable or out-of-bounds value falls back to the catalog
// default rather than refusing the sign-in, and the remember flag is kept on
// the signature because the store's column still records the caller's
// choice.
func (s *Service) SessionLifetime(ctx context.Context, remember bool) time.Duration {
	return s.sessionLifetime(ctx)
}

// sessionSettings reads the session bound and the second-factor gate at
// mint time. *appconfig.Settings satisfies it; the interface keeps the
// appconfig feature out of this one's import graph.
type sessionSettings interface {
	GetInt64(ctx context.Context, key string) (int64, error)
	GetBool(ctx context.Context, key string) (bool, error)
}

// SettingSessionMaxLifetime is the catalog key the session bound reads. The
// catalog owns the name; this constant is how this package spells it.
const SettingSessionMaxLifetime = "session.max_lifetime"

// SettingMFARequired is the catalog key the global second-factor gate reads.
// The catalog owns the name; this constant is how this package spells it.
const SettingMFARequired = "mfa.required"

// The catalog's default for the bound, mirrored so a nil settings feature or
// an unreadable read still mints a session the deployment set out with.
const sessionMaxLifetimeDefault = 604800 * time.Second

// The spec's bounds for the remembered session: five minutes to ten years.
const (
	sessionLifetimeFloor = 5 * 60 * time.Second
	sessionLifetimeCeil  = 10 * 365 * 24 * time.Hour
)

// WithSessionSettings wires the runtime bound after construction. Nil keeps
// the catalog default — the state a test or a bare wiring is in.
func (s *Service) WithSessionSettings(reader sessionSettings) *Service {
	s.settings = reader
	return s
}

// WithPasswordPolicy wires the breach check's runtime source. Nil arms no
// flag — the state a test or a bare wiring is in.
func (s *Service) WithPasswordPolicy(policy *password.Validator) *Service {
	s.policy = policy
	return s
}

// sessionLifetime reads the bound fresh at every mint. An unreadable or
// out-of-bounds value falls back to the catalog default: a setting the
// database cannot answer must not end every sign-in.
func (s *Service) sessionLifetime(ctx context.Context) time.Duration {
	if s.settings == nil {
		return sessionMaxLifetimeDefault
	}
	seconds, err := s.settings.GetInt64(ctx, SettingSessionMaxLifetime)
	if err != nil {
		s.log.Warn("signin: session.max_lifetime unreadable; using the default", slog.Any("error", err))
		return sessionMaxLifetimeDefault
	}
	lifetime := time.Duration(seconds) * time.Second
	if lifetime < sessionLifetimeFloor || lifetime > sessionLifetimeCeil {
		s.log.Warn("signin: session.max_lifetime out of bounds; using the default", slog.Int64("seconds", seconds))
		return sessionMaxLifetimeDefault
	}
	return lifetime
}

// mfaRequired reads the global second-factor gate. An unreadable value
// answers the catalog default — the gate ships off, and a setting the
// database cannot answer must not end every sign-in.
func (s *Service) mfaRequired(ctx context.Context) bool {
	if s.settings == nil {
		return false
	}
	required, err := s.settings.GetBool(ctx, SettingMFARequired)
	if err != nil {
		s.log.Warn("signin: mfa.required unreadable; the gate is off", slog.Any("error", err))
		return false
	}
	return required
}

// SignAccessToken mints the stateless token for an account the caller has
// already read: the claims are the account's identity, the subject is the
// account's identifier, and the session identifier is what the caller carries
// in `sid`. It is the shape the renewal signs through, and it exists so the
// two issuers — the opening and the renewal — cannot drift apart.
//
// The grants are read here rather than carried on the account row, so every
// door that mints a token answers the same authorization snapshot from the
// same query: a role assigned between the account read and this call is in
// the token, and one revoked as late as this call is out of it.
func (s *Service) SignAccessToken(ctx context.Context, account *Account, sessionID session.SessionID, now time.Time) (string, error) {
	roles, permissions, err := user.LoadGrants(ctx, s.pool, account.ID)
	if err != nil {
		return "", err
	}
	return s.SignSessionToken(ctx, user.FormatID(account.ID), jwtutils.AccessClaims{
		Email:       account.Email,
		Username:    account.Username,
		DisplayName: account.DisplayName,
		Roles:       roles,
		Permissions: permissions,
		SessionID:   sessionID.String(),
	}, sessionID, now)
}

// SignSessionToken mints the stateless token from the claims the caller
// assembled. The key and algorithm resolve on every call rather than at
// construction, so a rotation is picked up without a restart. It is exported
// because a renewal signs the same claims for the same session, and the two
// issuers must not drift apart.
func (s *Service) SignSessionToken(ctx context.Context, subject string, claims jwtutils.AccessClaims, sessionID session.SessionID, now time.Time) (string, error) {
	algorithm, err := s.keys.SigningAlgorithm()
	if err != nil {
		return "", fmt.Errorf("signin: signing algorithm: %w", err)
	}
	key, err := s.signingKey(ctx, algorithm)
	if err != nil {
		return "", fmt.Errorf("signin: signing key: %w", err)
	}

	signer, err := jwtutils.NewSigner[jwtutils.AccessClaims](key, algorithm)
	if err != nil {
		return "", fmt.Errorf("signin: signer: %w", err)
	}
	signer = signer.WithIssuer(s.issuer).WithTTL(s.accessTTL)

	token, err := signer.Sign(claims, jwtutils.Standard{
		Subject:   subject,
		IssuedAt:  now,
		NotBefore: now,
	})
	if err != nil {
		return "", fmt.Errorf("signin: sign access token: %w", err)
	}
	return token, nil
}

// signingKey picks the half of the dual stack the resolved algorithm names: a
// symmetric algorithm signs with the HMAC secret, anything else with the
// configured key pair.
func (s *Service) signingKey(ctx context.Context, algorithm jwa.SignatureAlgorithm) (jwk.Key, error) {
	if algorithm.IsSymmetric() {
		return s.keys.HMACKey(ctx)
	}
	return s.keys.SignKey(ctx)
}

// TokenPair is the refresh token the caller sees beside the hash the session
// row stores. It is crypto.RefreshTokenPair spelled in this package's
// vocabulary, so a caller of the issuer never reaches past the issuer.
type TokenPair = crypto.RefreshTokenPair

// NewRefreshToken draws the token the caller sees and returns it beside the
// hash the session row stores. The draw lives in pkg/crypto, because the
// renewal draws a replacement the same way and a token's entropy must not
// depend on which procedure issued it.
func NewRefreshToken() (TokenPair, error) {
	pair, err := crypto.NewRefreshTokenPair()
	return TokenPair(pair), err
}

// verifyDummy runs the password verifier against a hash of nothing, so a
// sign-in of an unknown account does the same hashing work as a wrong
// password and the two failures are indistinguishable by timing.
func (s *Service) verifyDummy(password string) {
	hash, ok := s.dummyHash.get(func() (string, error) {
		return s.hasher.Hash("tango-dummy-account")
	})
	if ok {
		match, _ := s.hasher.Verify(password, hash)
		_ = match
	}
}

// once computes a value at most once; a failure is retried on the next call.
type once[T any] struct {
	mu    sync.Mutex
	value T
	done  bool
}

func (o *once[T]) get(fn func() (T, error)) (T, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.done {
		value, err := fn()
		if err != nil {
			return o.value, false
		}
		o.value, o.done = value, true
	}
	return o.value, true
}

// addrPtr converts the caller address to the INET column's form, nil when the
// transport did not supply one.
func addrPtr(raw string) *netip.Addr {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	return &addr
}
