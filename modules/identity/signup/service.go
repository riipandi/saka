package signup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"uuid"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/modules/identity/blocklist"
	"github.com/riipandi/saka/modules/identity/password"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/modules/identity/usergroup"
	"github.com/riipandi/saka/pkg/crypto"
)

// The failures a sign-up reports. The handler maps them to connect codes, so
// the wire form of a refusal lives with the transport, not here.
var (
	// ErrInvalidToken covers an unknown, expired, and spent signup token:
	// answering differently would tell a caller which half was wrong.
	ErrInvalidToken = errors.New("signup: invalid signup token")

	// ErrAccountExists covers a taken username and a taken email. The
	// username is matched case-insensitively, the way its unique index is.
	ErrAccountExists = errors.New("signup: account already exists")

	// ErrTokenNotFound is a delete whose id names no issued token.
	ErrTokenNotFound = errors.New("signup: signup token not found")

	// ErrGroupNotFound is a token issue naming a group that does not exist.
	ErrGroupNotFound = errors.New("signup: user group not found")

	// ErrSignupNotAllowed is an open-mode sign-up the allowlist refuses.
	// The answer is shaped so it names nothing about the account the
	// address may already hold.
	ErrSignupNotAllowed = errors.New("signup: not allowed")

	// ErrUsernameRequired is a sign-up the require-username toggle refuses
	// for carrying no username.
	ErrUsernameRequired = errors.New("signup: username is required")

	// ErrUsernameInvalid is a username that carries the shape the column's
	// check enforces — the pattern applies only when the field is present.
	ErrUsernameInvalid = errors.New("signup: username is invalid")
)

// errEmailCollision is the strict mode's internal sentinel: the insert
// failed on the email's unique index and the caller earns the success
// shape. It never escapes the service — the WithTx boundary resolves it
// into the decoy answer.
var errEmailCollision = errors.New("signup: the email is taken (strict)")

// Service creates an account from a signup token.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// audit writes the record of the account this flow creates, in the same
	// transaction, so the account and its record commit together.
	audit  *audit.Recorder
	hasher *crypto.PasswordHasher
	log    *slog.Logger
	now    func() time.Time
	// settings reads the access mode, the identity toggles, the allowlist,
	// and the verification gate at call time. Nil runs the bare-wiring
	// policy — invite mode (the historical token-required path), toggles
	// off, the account created verified — the state a test or an unwired
	// deployment is in; the composition root always wires the catalog.
	settings settingsReader
	// verifier issues the email-verification code inside the sign-up's
	// transaction and delivers the message after it commits. Nil keeps the
	// account unverified with no code — the state a bare wiring is in.
	verifier VerificationIssuer
	// policy is the credential check's runtime source. Nil keeps the
	// static policy.
	policy *password.Validator
	// blocklist is the blocked-identifier gate the area wires after
	// construction. Nil leaves the gate open — the state a test or a bare
	// wiring is in — and the toggle decides whether the wired gate reads.
	blocklist BlocklistChecker
	// notices is the strict mode's seam: the owner's notice a taken-email
	// sign-up sends. Nil skips the mail; the strict answer itself is the
	// policy's, not the seam's.
	notices EnumerationNotifier
}

// settingsReader is the sign-up policy's runtime source: the access mode,
// the allowlist, the identity toggles, and the verification gate read fresh
// at every sign-up, so an operator's change lands without a restart.
// *appconfig.Settings satisfies it; the interface keeps the appconfig
// feature out of this one's import graph.
type settingsReader interface {
	GetString(ctx context.Context, key string) (string, error)
	GetBool(ctx context.Context, key string) (bool, error)
}

// The catalog keys the sign-up reads. The catalog owns the names; these
// constants are how this package spells them.
const (
	SettingAccessMode              = "access.mode"
	SettingAccessAllowlistEnabled  = "access.allowlist_enabled"
	SettingAccessAllowlist         = "access.allowlist"
	SettingAccessBlocklistEnabled  = "access.blocklist_enabled"
	SettingAccessBlockSubaddresses = "access.block_email_subaddresses"
	SettingSignupUsernameEnabled   = "auth.signup_username_enabled"
	SettingRequireUsername         = "auth.require_username"
	SettingVerifyEmailAtSignup     = "auth.verify_email_at_signup"
	// SettingUserEnumerationProtection is the strict mode's key: strict, a
	// taken email earns the success shape instead of the refusal.
	SettingUserEnumerationProtection = "auth.user_enumeration_protection"
)

// VerificationIssuer is the email-verification seam: the code's row belongs
// in the sign-up's transaction, the message leaves once it commits.
type VerificationIssuer interface {
	// IssueForSignup draws the single-use code, writes its hash for the
	// account inside the caller's transaction, and answers the raw value.
	IssueForSignup(ctx context.Context, tx datastore.Querier, userID uuid.UUID, email, displayName string) (string, error)
	// DeliverForSignup enqueues the message for a code whose row committed.
	DeliverForSignup(ctx context.Context, userID uuid.UUID, email, displayName, rawToken string) error
}

// WithSignupSettings wires the runtime policy after construction. Nil keeps
// the catalog defaults — the state a test or a bare wiring is in.
func (s *Service) WithSignupSettings(reader settingsReader) *Service {
	s.settings = reader
	return s
}

// WithVerification wires the code issuer. Nil keeps the account unverified
// with no code sent.
func (s *Service) WithVerification(issuer VerificationIssuer) *Service {
	s.verifier = issuer
	return s
}

// signupPolicy is the setting slice one sign-up reads, resolved once at the
// call's start so the branches see one world.
type signupPolicy struct {
	inviteMode bool
	// stampVerified is the verification gate's other side: off, the account
	// leaves with the column stamped — it may sign in at once; on, the
	// column stays empty and the outstanding code owns the gate.
	stampVerified     bool
	allowlistOn       bool
	allowlist         []string
	blocklistOn       bool
	blockSubaddresses bool
	usernameOn        bool
	requireUsername   bool
	verifyEmail       bool
	// strict is the enumeration-protection mode: on, a taken email earns
	// the success shape instead of the refusal.
	strict bool
}

// policy reads the catalog. The bare wiring answers the historical policy —
// invite mode, account created verified — and an unreadable key keeps its
// catalog default, so a settings row that cannot answer never rewrites the
// mode silently.
func (s *Service) policyOf(ctx context.Context) signupPolicy {
	p := signupPolicy{inviteMode: true, stampVerified: true}
	if s.settings == nil {
		return p
	}
	if mode, err := s.settings.GetString(ctx, SettingAccessMode); err != nil {
		s.log.WarnContext(ctx, "signup: access.mode unreadable; using the default", "error", err)
	} else {
		p.inviteMode = mode == "invite"
	}
	if on, err := s.settings.GetBool(ctx, SettingAccessAllowlistEnabled); err != nil {
		s.log.WarnContext(ctx, "signup: access.allowlist_enabled unreadable; using the default", "error", err)
	} else {
		p.allowlistOn = on
	}
	if p.allowlistOn {
		if list, err := s.settings.GetString(ctx, SettingAccessAllowlist); err != nil {
			s.log.WarnContext(ctx, "signup: access.allowlist unreadable; treating as empty", "error", err)
		} else {
			p.allowlist = blocklist.SplitAllowlist(list)
		}
	}
	if on, err := s.settings.GetBool(ctx, SettingAccessBlocklistEnabled); err != nil {
		s.log.WarnContext(ctx, "signup: access.blocklist_enabled unreadable; using the default", "error", err)
	} else {
		p.blocklistOn = on
	}
	if on, err := s.settings.GetBool(ctx, SettingAccessBlockSubaddresses); err != nil {
		s.log.WarnContext(ctx, "signup: access.block_email_subaddresses unreadable; using the default", "error", err)
	} else {
		p.blockSubaddresses = on
	}
	if on, err := s.settings.GetBool(ctx, SettingSignupUsernameEnabled); err != nil {
		s.log.WarnContext(ctx, "signup: auth.signup_username_enabled unreadable; using the default", "error", err)
	} else {
		p.usernameOn = on
	}
	if on, err := s.settings.GetBool(ctx, SettingRequireUsername); err != nil {
		s.log.WarnContext(ctx, "signup: auth.require_username unreadable; using the default", "error", err)
	} else {
		p.requireUsername = on
	}
	if on, err := s.settings.GetBool(ctx, SettingVerifyEmailAtSignup); err != nil {
		s.log.WarnContext(ctx, "signup: auth.verify_email_at_signup unreadable; using the default", "error", err)
	} else {
		p.verifyEmail = on
		p.stampVerified = !on
	}
	if mode, err := s.settings.GetString(ctx, SettingUserEnumerationProtection); err != nil {
		s.log.WarnContext(ctx, "signup: auth.user_enumeration_protection unreadable; using the default", "error", err)
	} else {
		p.strict = mode == "strict"
	}
	return p
}

// usernamePattern mirrors the column's check: 3-32 ASCII letters, digits,
// and underscores.
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)

// stampVerifiedAt answers the instant the verification column carries: nil
// while the gate arms — the outstanding code owns it — the creation instant
// when the account may sign in at once.
func (p signupPolicy) stampVerifiedAt(at time.Time) *time.Time {
	if p.verifyEmail {
		return nil
	}
	return &at
}

// NewService builds the service. The database writes run in one transaction
// the service opens over the pool, so the account, its credential, and the
// token's use commit together or not at all.
func NewService(pool *datastore.Postgres, recorder *audit.Recorder, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		pool:   pool,
		repo:   NewRepository(),
		audit:  recorder,
		hasher: crypto.NewPasswordHasher(),
		log:    log,
		now:    time.Now,
	}
}

// Params carries one sign-up attempt.
type Params struct {
	Username  string
	Email     string
	Password  string
	Token     string
	FirstName string
	LastName  string
}

// WithPasswordPolicy wires the settings-driven validator. Nil keeps the
// static policy — the state a test or a bare wiring is in.
func (s *Service) WithPasswordPolicy(policy *password.Validator) *Service {
	s.policy = policy
	return s
}

// BlocklistChecker is the blocklist seam: the two questions the sign-up
// gates ask of the feature that owns the entries and the address shapes —
// whether the blocklist names the address, and whether its base is one an
// account already holds. The interface is this package's — the consuming
// side defines it — and the identity area satisfies it with the blocklist
// service after construction.
type BlocklistChecker interface {
	Blocked(ctx context.Context, address string) (bool, error)
	CollisionTaken(ctx context.Context, address string) (bool, error)
}

// WithBlocklist wires the blocked-identifier gate. Nil keeps the gate open.
func (s *Service) WithBlocklist(checker BlocklistChecker) *Service {
	s.blocklist = checker
	return s
}

// EnumerationNotifier is the strict mode's seam: the notice the address on
// file receives when an unknown caller signs up with it. The interface is
// this package's — the consuming side defines it — and the queue-backed
// adapter is wired in the area's Package.
type EnumerationNotifier interface {
	// EnqueueSignupAttemptExistingEmail tells the address's owner the
	// attempt happened. It carries no code and grants nothing.
	EnqueueSignupAttemptExistingEmail(ctx context.Context, email, displayName string)
}

// WithExistingEmailNotifier wires the strict mode's notice. Nil keeps the
// answer and skips the mail.
func (s *Service) WithExistingEmailNotifier(notices EnumerationNotifier) *Service {
	s.notices = notices
	return s
}

// blocklistBlocked asks the wired gate. An unwired gate never blocks: the
// bare wiring runs without the feature, the same state its tests build.
func (s *Service) blocklistBlocked(ctx context.Context, address string) (bool, error) {
	if s.blocklist == nil {
		return false, nil
	}
	return s.blocklist.Blocked(ctx, address)
}

// subaddressCollision asks the wired gate's other question. An unwired gate
// never blocks, the same state its tests build.
func (s *Service) subaddressCollision(ctx context.Context, address string) (bool, error) {
	if s.blocklist == nil {
		return false, nil
	}
	return s.blocklist.CollisionTaken(ctx, address)
}

// validatePassword runs the credential through the wired policy.
func (s *Service) validatePassword(ctx context.Context, clearText string) error {
	if s.policy != nil {
		return s.policy.Validate(ctx, clearText)
	}
	return password.Validate(clearText)
}

// Signup creates the account. The settings own the shape: the access mode
// decides whether a token is consumed, the identity toggles decide whether
// a username is required, the verification gate decides whether the account
// leaves with a code outstanding. The refusals run cheapest-first — the
// policy, the allowlist, then the token row, and only then the KDF — and
// one transaction commits the account, its credential, the token's use, and
// the verification code together.
func (s *Service) Signup(ctx context.Context, params Params) (user.UserView, error) {
	if validateErr := s.validatePassword(ctx, params.Password); validateErr != nil {
		return user.UserView{}, validateErr
	}

	p := s.policyOf(ctx)

	// The username is optional by the toggles; present, it carries the
	// column's pattern; absent, it is refused only when the require-username
	// toggle says so — and that toggle only means anything while the
	// username identity itself is on.
	username := strings.TrimSpace(params.Username)
	if username != "" {
		if !usernamePattern.MatchString(username) {
			return user.UserView{}, ErrUsernameInvalid
		}
	} else if p.requireUsername && p.usernameOn {
		return user.UserView{}, ErrUsernameRequired
	}

	// The allowlist and the blocklist are open-mode filters, checked before
	// anything touches the database: a refused address earns a cheap,
	// account-blind refusal. The allowlist wins the two lists' conflict —
	// an address it accepts passes the blocklist, so its answer decides
	// both gates — and a blocklist that cannot be read lets the sign-up
	// pass: a broken read must never lock the deployment out of its own
	// door.
	if !p.inviteMode {
		accepted := p.allowlistOn && blocklist.Allowlisted(params.Email, p.allowlist)
		if p.allowlistOn && !accepted {
			return user.UserView{}, ErrSignupNotAllowed
		}
		if !accepted && p.blocklistOn {
			blocked, blockedErr := s.blocklistBlocked(ctx, params.Email)
			if blockedErr != nil {
				s.log.WarnContext(ctx, "signup: the blocklist is unreadable; letting the sign-up pass", "error", blockedErr)
			} else if blocked {
				return user.UserView{}, ErrSignupNotAllowed
			}
		}
		// The subaddress blocker is a different rule with a different
		// shape: an address whose base an account already holds is
		// refused — the first sign-up for a base passes, so the mass
		// creation the feature targets is what it stops — and its
		// collision scan fails open like the lists do.
		if !accepted && p.blockSubaddresses {
			collides, collisionErr := s.subaddressCollision(ctx, params.Email)
			if collisionErr != nil {
				s.log.WarnContext(ctx, "signup: the subaddress collision scan failed; letting the sign-up pass", "error", collisionErr)
			} else if collides {
				return user.UserView{}, ErrSignupNotAllowed
			}
		}
	}

	name := displayName(params.FirstName, params.LastName)
	if name == "" {
		// The display name rejects an empty string; an account carrying no
		// names and no username falls back to the address's local part.
		name = params.Email
		if at := strings.IndexByte(params.Email, '@'); at > 0 {
			name = params.Email[:at]
		}
	}

	var created user.UserView
	var pendingVerification *pendingCode
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		var groupIDs []uuid.UUID
		var tokenID uuid.UUID
		if p.inviteMode {
			// The token is looked up before the password is hashed: a public
			// endpoint answers a junk token with a cheap refusal, not a KDF
			// run. The open mode skips the lookup entirely.
			token, findErr := s.repo.FindSignupTokenByHash(ctx, tx, crypto.HashHexToken(params.Token))
			if errors.Is(findErr, datastore.ErrNoRows) {
				return ErrInvalidToken
			}
			if findErr != nil {
				return findErr
			}
			if token.ExpiresAt.Before(s.now()) || token.UsageCount >= token.UsageLimit {
				return ErrInvalidToken
			}
			tokenID = token.ID
			groupIDs = token.GroupIDs
		}

		verifiedAt := p.stampVerifiedAt(s.now())
		userID, createErr := s.repo.CreateUser(ctx, tx, user.UserSchema{
			Username:        username,
			Email:           params.Email,
			FirstName:       params.FirstName,
			LastName:        params.LastName,
			DisplayName:     name,
			EmailVerifiedAt: verifiedAt,
		})
		if errUniqueViolation(createErr) {
			// The username's refusal stays honest in both modes: a username
			// is not a verified contact channel, and there is nothing to
			// leak past the name the caller typed. The email's is where the
			// strict mode moves the answer — the caller earns the success
			// shape and the address on file earns the notice. An unparseable
			// constraint falls back to the refusal: the safe answer.
			if p.strict && uniqueViolationOn(createErr, "users_email_key") {
				return errEmailCollision
			}
			return ErrAccountExists
		}
		if createErr != nil {
			return fmt.Errorf("signup: create user: %w", createErr)
		}

		// The hash runs inside the transaction the account opens in: a
		// token that failed above never paid for a KDF, and an account that
		// failed here never hashed for nothing.
		passwordHash, hashErr := s.hasher.Hash(params.Password)
		if hashErr != nil {
			return fmt.Errorf("signup: hash password: %w", hashErr)
		}
		if passErr := s.repo.CreatePassword(ctx, tx, password.UserPasswordSchema{
			UserID:       userID,
			PasswordHash: passwordHash,
		}); passErr != nil {
			return fmt.Errorf("signup: create password: %w", passErr)
		}

		if p.inviteMode {
			if consumeErr := s.repo.ConsumeSignupToken(ctx, tx, tokenID, s.now()); consumeErr != nil {
				return consumeErr
			}

			// The token's groups take the account in. A group deleted after
			// the token was issued joins fewer accounts rather than failing
			// the sign-up.
			if len(groupIDs) > 0 {
				if joinErr := s.repo.AddUserToGroups(ctx, tx, userID, groupIDs); joinErr != nil {
					return joinErr
				}
			}
		}

		// The verification gate leaves the account unverified — the column's
		// default — with the code's row inside this same transaction, so the
		// account and its outstanding code commit together. The message
		// leaves after the commit: a task queue is durable on its own, and
		// the record of a message the transaction rolled back would
		// understate what happened.
		if p.verifyEmail && s.verifier != nil {
			raw, issueErr := s.verifier.IssueForSignup(ctx, tx, userID, params.Email, name)
			if issueErr != nil {
				return issueErr
			}
			pendingVerification = &pendingCode{
				UserID:      userID,
				Email:       params.Email,
				DisplayName: name,
				RawToken:    raw,
			}
		}

		// The database fills the columns the insert omits, so the answer
		// is the account read back — the same canonical view the account
		// procedures answer with.
		read, readErr := user.ReadAccount(ctx, tx, userID)
		if readErr != nil {
			return readErr
		}
		created = read

		// The account, its credential, the token's use, and this record
		// commit together: a rolled-back sign-up must not leave a record
		// claiming an account exists.
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventAccountCreated,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"username": read.Username,
				"email":    read.Email,
				"source":   "signup",
			},
		})
		return nil
	})
	if err != nil {
		if !errors.Is(err, errEmailCollision) {
			return user.UserView{}, err
		}
		// The strict answer. The collision was the insert's verdict; the
		// read on the pool names the account the notice goes to — a row
		// deleted between the two answers the honest refusal instead.
		existing, findErr := s.repo.FindUserEmailAccount(ctx, s.pool, params.Email)
		if findErr != nil {
			return user.UserView{}, ErrAccountExists
		}
		if s.notices != nil {
			s.notices.EnqueueSignupAttemptExistingEmail(ctx, params.Email, existing.DisplayName)
		}
		return s.decoyView(params, name, p), nil
	}
	if pendingVerification != nil {
		if deliverErr := s.verifier.DeliverForSignup(ctx, pendingVerification.UserID, pendingVerification.Email, pendingVerification.DisplayName, pendingVerification.RawToken); deliverErr != nil {
			// The account exists and its code is stored; a delivery
			// failure is a message problem, not a sign-up failure. The
			// account re-requests the code through the resend procedure.
			s.log.ErrorContext(ctx, "signup: verification delivery failed", "error", deliverErr)
		}
	}
	return created, nil
}

// pendingCode carries the verification a sign-up issued, from the
// transaction that stored it to the delivery that follows the commit.
type pendingCode struct {
	UserID      uuid.UUID
	Email       string
	DisplayName string
	RawToken    string
}

// decoyView builds the success answer the strict mode serves for a taken
// email: the fields the caller itself supplied, a fresh identifier, and the
// verification state the honest path would have stamped — the shape a
// fresh sign-up answers with, describing no account. The identifier is
// minted, not read: a zero value would be the oracle the mode exists to
// close.
func (s *Service) decoyView(params Params, name string, p signupPolicy) user.UserView {
	view := user.UserView{
		ID:            user.FormatID(uuid.NewV7()),
		Username:      params.Username,
		Email:         params.Email,
		DisplayName:   name,
		Timezone:      user.DefaultTimezone,
		Disabled:      false,
		EmailVerified: p.stampVerifiedAt(s.now()) != nil,
		CreatedAt:     s.now(),
	}
	if params.FirstName != "" {
		view.FirstName = &params.FirstName
	}
	if params.LastName != "" {
		view.LastName = &params.LastName
	}
	return view
}

// displayName composes the name the UI shows from the optional given and
// family names, falling back to the username when neither is carried. The
// account-creating features share the rule; the user package owns it.
var displayName = user.DisplayName

// CreateTokenParams carries one token issue. The window and budget bounds
// live in the contract — `CreateSignupTokenRequest` carries them as
// protovalidate constraints the transport's validate interceptor enforces
// before this runs — so the service applies only the unset-budget default.
// GroupIDs are the wire identifiers of the groups every account this token
// creates joins.
type CreateTokenParams struct {
	TTL        time.Duration
	UsageLimit int32    // zero means the single-invitation default
	GroupIDs   []string // empty means the sign-ups join no group
}

// TokenView is an issued token as the procedures answer it: the counters and
// the window, never the raw value.
type TokenView struct {
	ID         string
	UsageLimit int32
	UsageCount int32
	CreatedAt  time.Time
	ExpiresAt  time.Time
	GroupIDs   []string
}

// CreatedToken is the issued token and the raw value shown once.
type CreatedToken struct {
	Token    TokenView
	RawToken string
}

// CreateSignupToken issues a token: the raw value is drawn here, shown once
// in the answer, and only its hash is stored. A usage limit of zero means
// the single invitation. The token, its group links, and the group check
// commit together, so a token never names a group the account creation
// cannot resolve.
func (s *Service) CreateSignupToken(ctx context.Context, params CreateTokenParams) (CreatedToken, error) {
	if params.UsageLimit == 0 {
		params.UsageLimit = 1
	}

	rawToken, err := crypto.NewHexToken()
	if err != nil {
		return CreatedToken{}, fmt.Errorf("signup: token: %w", err)
	}
	now := s.now()

	// The wire identifiers become row identifiers before the insert, so an
	// id that names nothing is refused here and not halfway the join.
	groupIDs := make([]uuid.UUID, 0, len(params.GroupIDs))
	for _, wire := range params.GroupIDs {
		groupID, parseErr := usergroup.UUIDFromWire(wire)
		if parseErr != nil {
			return CreatedToken{}, fmt.Errorf("%w: %s", ErrGroupNotFound, wire)
		}
		groupIDs = append(groupIDs, groupID)
	}

	var id uuid.UUID
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if len(groupIDs) > 0 {
			if existsErr := s.repo.AssertGroupsExist(ctx, tx, groupIDs); existsErr != nil {
				return existsErr
			}
		}
		created, createErr := s.repo.CreateSignupToken(ctx, tx, crypto.HashHexToken(rawToken), params.UsageLimit, now.Add(params.TTL), groupIDs)
		if createErr != nil {
			return createErr
		}
		id = created
		return nil
	})
	if err != nil {
		return CreatedToken{}, err
	}
	return CreatedToken{
		Token: TokenView{
			ID:         id.String(),
			UsageLimit: params.UsageLimit,
			UsageCount: 0,
			CreatedAt:  now,
			ExpiresAt:  now.Add(params.TTL),
			GroupIDs:   params.GroupIDs,
		},
		RawToken: rawToken,
	}, nil
}

// ListSignupTokens answers one page of the issued tokens, ordered as the
// caller asked (absent a choice, newest first), with the pagination metadata
// the response carries.
func (s *Service) ListSignupTokens(ctx context.Context, sortBy string, ascending bool, page, limit int) ([]TokenView, webutil.Pagination, error) {
	page, limit = webutil.NormalizePage(page, limit, webutil.DefaultPageSize, webutil.MaxPageSize)

	tokens, total, err := s.repo.ListSignupTokens(ctx, s.pool, sortBy, ascending, webutil.Offset(page, limit), limit)
	if err != nil {
		return nil, webutil.Pagination{}, err
	}

	views := make([]TokenView, 0, len(tokens))
	for _, row := range tokens {
		groups := make([]string, 0, len(row.GroupIDs))
		for _, groupID := range row.GroupIDs {
			groups = append(groups, usergroup.FormatID(groupID))
		}
		views = append(views, TokenView{
			ID:         row.ID.String(),
			UsageLimit: row.UsageLimit,
			UsageCount: row.UsageCount,
			CreatedAt:  row.CreatedAt,
			ExpiresAt:  row.ExpiresAt,
			GroupIDs:   groups,
		})
	}
	return views, webutil.NewPagination(webutil.PaginationParams{Page: page, Limit: limit}, total), nil
}

// DeleteSignupToken revokes an issued token. A token that named nothing is
// the not-found failure, so a withdrawn invitation is told from a typo.
func (s *Service) DeleteSignupToken(ctx context.Context, id string) error {
	tokenID, err := uuid.Parse(id)
	if err != nil {
		return ErrTokenNotFound
	}
	deleted, err := s.repo.DeleteSignupToken(ctx, s.pool, tokenID)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrTokenNotFound
	}
	return nil
}
