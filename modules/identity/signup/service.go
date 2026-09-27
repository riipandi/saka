package signup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/password"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/modules/identity/usergroup"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/responder"
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
)

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
} // Signup consumes the token and creates the account. The request's shape is
// the contract's business — `SignupRequest` carries the constraints the
// transport's validate interceptor enforces before this runs.
//
// Email verification is a later procedure: the account is created unverified,
// which is the column's default and needs no code here yet. The password is
// hashed and stored with the account, so the account can sign in immediately.
func (s *Service) Signup(ctx context.Context, params Params) (user.UserView, error) {
	if policyErr := password.Validate(params.Password); policyErr != nil {
		return user.UserView{}, policyErr
	}
	// The token is looked up before the password is hashed: a public
	// endpoint answers a junk token with a cheap refusal, not a KDF run.
	tokenHash := crypto.HashHexToken(params.Token)

	name := displayName(params.FirstName, params.LastName)

	var created user.UserView
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		token, findErr := s.repo.FindSignupTokenByHash(ctx, tx, tokenHash)
		if errors.Is(findErr, datastore.ErrNoRows) {
			return ErrInvalidToken
		}
		if findErr != nil {
			return findErr
		}
		if token.ExpiresAt.Before(s.now()) || token.UsageCount >= token.UsageLimit {
			return ErrInvalidToken
		}

		userID, createErr := s.repo.CreateUser(ctx, tx, user.UserSchema{
			Username:    params.Username,
			Email:       params.Email,
			FirstName:   params.FirstName,
			LastName:    params.LastName,
			DisplayName: name,
		})
		if errUniqueViolation(createErr) {
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

		if consumeErr := s.repo.ConsumeSignupToken(ctx, tx, token.ID, s.now()); consumeErr != nil {
			return consumeErr
		}

		// The token's groups take the account in. A group deleted after
		// the token was issued joins fewer accounts rather than failing
		// the sign-up.
		if len(token.GroupIDs) > 0 {
			if joinErr := s.repo.AddUserToGroups(ctx, tx, userID, token.GroupIDs); joinErr != nil {
				return joinErr
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
		return user.UserView{}, err
	}
	return created, nil
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
func (s *Service) ListSignupTokens(ctx context.Context, sortBy string, ascending bool, page, limit int) ([]TokenView, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)

	tokens, total, err := s.repo.ListSignupTokens(ctx, s.pool, sortBy, ascending, responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
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
	return views, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
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
