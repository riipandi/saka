package apikey

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"uuid"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// The shape of a presented key: an eight-character prefix an operator reads,
// a separator, and a thirty-two character secret a client sends. Both halves
// draw from the full alphanumeric alphabet — ambiguity costs nothing in a
// value a client holds whole, unlike a code a holder types from an email.
const (
	prefixLength = 8
	secretLength = 32
	keySeparator = "."
)

// The failures the service defines. The handler maps them onto the codes the
// Connect protocol carries; the service defines what happened, not how it is
// answered.
var (
	// ErrKeyNotFound is an identifier that names no key, or a key another
	// account owns — the two are the same answer, because the owner's list
	// is the only way to learn which keys exist.
	ErrKeyNotFound = errors.New("apikey: key not found")

	// ErrKeyExists is a name the owner's unique index already holds.
	ErrKeyExists = errors.New("apikey: key name already in use")

	// ErrKeyNotExpired is a renewal of a key whose window is still open.
	// Renewal is how a key lives past its expiry, not how it escapes one.
	ErrKeyNotExpired = errors.New("apikey: the key has not expired yet")

	// ErrOwnerDisabled is a presented key whose owner cannot sign in. The
	// key is fine; the account it acts as is not.
	ErrOwnerDisabled = errors.New("apikey: the key's owner is disabled")
)

// Service carries the rules of the machine credentials: how a key is drawn,
// when it authenticates, and what a change records. The repository carries
// the SQL.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// audit writes the record of every key change, in the transaction that
	// changes the key. A revocation and the record of it commit together, so
	// the log cannot describe a key that is still live.
	audit *fwaudit.Recorder
	log   *slog.Logger

	// now is the instant the service's decisions read. It is a field so a
	// test can hold the clock still — or move it — without waiting out a
	// window the database's own check refuses to write.
	now func() time.Time
}

// NewService builds the service over the shared pool.
func NewService(pool *datastore.Postgres, recorder *fwaudit.Recorder, log *slog.Logger) *Service {
	return &Service{
		pool:  pool,
		repo:  NewRepository(),
		audit: recorder,
		log:   log,
		now:   time.Now,
	}
}

// CreateParams carries the fields a key is made of.
type CreateParams struct {
	Name        string
	Description string
	// ExpiresAt is when the key stops authenticating. A window that closes
	// before it opens is refused: a key born expired authenticates nothing.
	ExpiresAt time.Time
}

// Issued is what a create and a renewal answer: the key's row and the raw
// credential. The raw credential is stored nowhere — its hash is the row's
// whole presence — so this is the last it exists, and a lost one means a
// revocation and a new key.
type Issued struct {
	Key KeySchema
	Raw string
}

// Create issues a key for one account. The name is unique per owner by the
// index, read from the write's failure; the created row is read back inside
// the transaction so the answer carries what the database stored.
func (s *Service) Create(ctx context.Context, owner uuid.UUID, params CreateParams) (Issued, error) {
	raw, err := generateKey()
	if err != nil {
		return Issued{}, fmt.Errorf("apikey: generate key: %w", err)
	}

	var issued Issued
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		id, createErr := s.repo.CreateKey(ctx, tx, KeySchema{
			UserID:    owner,
			Name:      params.Name,
			Prefix:    prefixOf(raw),
			KeyHash:   crypto.HashTokenBytes(raw),
			Descr:     descriptionOf(params.Description),
			ExpiresAt: params.ExpiresAt,
		})
		if errUniqueViolation(createErr) {
			return ErrKeyExists
		}
		if createErr != nil {
			return fmt.Errorf("apikey: create: %w", createErr)
		}

		row, readErr := s.repo.GetKey(ctx, tx, id)
		if readErr != nil {
			return readErr
		}
		issued = Issued{Key: row, Raw: raw}

		s.audit.Record(ctx, tx, fwaudit.Entry{
			Event:        audit.EventAPIKeyCreated,
			Status:       fwaudit.StatusSuccess,
			UserID:       owner.String(),
			ResourceType: ResourceAPIKey,
			ResourceID:   id.String(),
			Payload: map[string]string{
				"name":       params.Name,
				"expires_at": params.ExpiresAt.Format(time.RFC3339),
			},
		})
		return nil
	})
	if err != nil {
		return Issued{}, err
	}
	return issued, nil
}

// ListOwn answers one page of the owner's keys, ordered as the caller asked
// (absent a choice, newest first).
func (s *Service) ListOwn(ctx context.Context, owner uuid.UUID, sortBy string, ascending bool, page, limit int) ([]KeySchema, webutil.Pagination, error) {
	page, limit = webutil.NormalizePage(page, limit, webutil.DefaultPageSize, webutil.MaxPageSize)
	return s.listPage(ctx, owner, sortBy, ascending, page, limit)
}

// ListAll answers one page of every key the deployment holds, ordered as the
// caller asked. It is the administrative view — the owner's own list is
// ListOwn.
func (s *Service) ListAll(ctx context.Context, sortBy string, ascending bool, page, limit int) ([]KeySchema, webutil.Pagination, error) {
	page, limit = webutil.NormalizePage(page, limit, webutil.DefaultPageSize, webutil.MaxPageSize)
	return s.listPage(ctx, uuid.Nil(), sortBy, ascending, page, limit)
}

// Renew replaces an expired key's secret and window. The key is read first:
// it is the ownership check and the expiry check, and the renewal's write is
// the one statement that replaces the credential, so a renewed key and the
// record of it commit together.
func (s *Service) Renew(ctx context.Context, owner, keyID uuid.UUID, expiresAt time.Time) (Issued, error) {
	raw, err := generateKey()
	if err != nil {
		return Issued{}, fmt.Errorf("apikey: generate key: %w", err)
	}

	var issued Issued
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, getErr := s.repo.GetKey(ctx, tx, keyID)
		if errors.Is(getErr, datastore.ErrNoRows) {
			return ErrKeyNotFound
		}
		if getErr != nil {
			return getErr
		}
		if row.UserID != owner {
			return ErrKeyNotFound
		}
		if !row.ExpiresAt.Before(s.now()) {
			return ErrKeyNotExpired
		}

		updated, renewErr := s.repo.RenewKey(ctx, tx, keyID, owner, crypto.HashTokenBytes(raw), expiresAt, s.now())
		if renewErr != nil {
			return renewErr
		}
		if !updated {
			return s.renewMiss(ctx, tx, owner, keyID)
		}

		fresh, readErr := s.repo.GetKey(ctx, tx, keyID)
		if readErr != nil {
			return readErr
		}
		issued = Issued{Key: fresh, Raw: raw}

		s.audit.Record(ctx, tx, fwaudit.Entry{
			Event:        audit.EventAPIKeyRenewed,
			Status:       fwaudit.StatusSuccess,
			UserID:       owner.String(),
			ResourceType: ResourceAPIKey,
			ResourceID:   keyID.String(),
			Payload: map[string]string{
				"name":       row.Name,
				"expires_at": expiresAt.Format(time.RFC3339),
			},
		})
		return nil
	})
	if err != nil {
		return Issued{}, err
	}
	return issued, nil
}

// renewMiss names why the conditional update matched nothing, using the same
// answers as the read that preceded it. A row that is gone, owned by someone
// else, or revoked is not found. A row whose window is open again — the other
// renew won — has not expired.
func (s *Service) renewMiss(ctx context.Context, db datastore.Querier, owner, keyID uuid.UUID) error {
	row, err := s.repo.GetKey(ctx, db, keyID)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrKeyNotFound
	}
	if err != nil {
		return err
	}
	if row.UserID != owner || row.RevokedAt != nil {
		return ErrKeyNotFound
	}
	if !row.ExpiresAt.Before(s.now()) {
		return ErrKeyNotExpired
	}
	return ErrKeyNotFound
}

// Revoke stamps one of the owner's keys revoked. The ownership check is the
// read; the write refuses an already-revoked row, and the service answers the
// state the caller asked for — revoking a revoked key is the same success.
func (s *Service) Revoke(ctx context.Context, owner, keyID uuid.UUID) error {
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, getErr := s.repo.GetKey(ctx, tx, keyID)
		if errors.Is(getErr, datastore.ErrNoRows) {
			return ErrKeyNotFound
		}
		if getErr != nil {
			return getErr
		}
		if row.UserID != owner {
			return ErrKeyNotFound
		}

		revoked, revokeErr := s.repo.RevokeKey(ctx, tx, keyID, owner, s.now())
		if revokeErr != nil {
			return revokeErr
		}
		if !revoked {
			// The key was already revoked: the caller's intent is the state
			// the key is in, so the request succeeds and records nothing.
			return nil
		}

		s.audit.Record(ctx, tx, fwaudit.Entry{
			Event:        audit.EventAPIKeyRevoked,
			Status:       fwaudit.StatusSuccess,
			UserID:       owner.String(),
			ResourceType: ResourceAPIKey,
			ResourceID:   keyID.String(),
			Payload: map[string]string{
				"name": row.Name,
			},
		})
		return nil
	})
}

// Validate is the authwall's one read: the account a presented X-API-Key
// value acts as. An unknown, expired, or revoked key answers the same
// not-found, so the refusal does not disclose which half failed. The owner's
// account is read live beside the key, so a role change or a disablement
// takes effect on the next request rather than at the next token mint; the
// last-used instant is a touch, not a decision.
func (s *Service) Validate(ctx context.Context, presented string) (user.UserSchema, error) {
	if presented == "" {
		return user.UserSchema{}, ErrKeyNotFound
	}

	now := s.now()
	key, owner, err := s.repo.FindActiveKey(ctx, s.pool, crypto.HashTokenBytes(presented), now)
	if errors.Is(err, datastore.ErrNoRows) {
		return user.UserSchema{}, ErrKeyNotFound
	}
	if err != nil {
		return user.UserSchema{}, err
	}

	// The touch is best-effort: the request is already authenticated, and a
	// metric that loses its write is not a refusal.
	s.repo.TouchLastUsed(ctx, s.pool, key.ID, now)

	return owner, nil
}

// AuthenticateAPIKey is the authwall's machine half: the caller a presented
// key acts as. The claims are the owner's account read live — the role
// change or the disablement takes effect on the next request — and the
// session identifier is empty, because no session exists behind the
// credential: the key is its own proof, and its row is the durable record.
//
// The credential kind travels on the caller, which is what the guard's
// session rule reads when the keys' own surface refuses the credential that
// would otherwise manage it.
func (s *Service) AuthenticateAPIKey(ctx context.Context, presented string) (*jwtutils.Caller, error) {
	owner, err := s.Validate(ctx, presented)
	if err != nil {
		return nil, err
	}
	roles, permissions, err := user.LoadGrants(ctx, s.pool, owner.ID)
	if err != nil {
		return nil, err
	}
	return &jwtutils.Caller{
		Email:       owner.Email,
		Username:    owner.Username,
		DisplayName: owner.DisplayName,
		Roles:       roles,
		Permissions: permissions,
		UserID:      user.FormatID(owner.ID),
		Credential:  jwtutils.CredentialAPIKey,
	}, nil
}

// listPage runs one list query and answers the page with its metadata. An
// owner identifier scopes the page; the zero value names every key.
func (s *Service) listPage(ctx context.Context, owner uuid.UUID, sortBy string, ascending bool, page, limit int) ([]KeySchema, webutil.Pagination, error) {
	keys, total, err := s.repo.ListKeys(ctx, s.pool, owner, sortBy, ascending, webutil.Offset(page, limit), limit)
	if err != nil {
		return nil, webutil.Pagination{}, err
	}
	return keys, webutil.NewPagination(webutil.PaginationParams{Page: page, Limit: limit}, total), nil
}

// generateKey draws one presented credential: an eight-character prefix an
// operator reads, a separator, and a thirty-two character secret a client
// sends. The draw is over the full alphanumeric alphabet with the crypto
// source, so neither half is a modulo bias away from uniform.
func generateKey() (string, error) {
	prefix, err := crypto.RandomString(prefixLength, crypto.AlphabetAlphanumeric)
	if err != nil {
		return "", err
	}
	secret, err := crypto.RandomString(secretLength, crypto.AlphabetAlphanumeric)
	if err != nil {
		return "", err
	}
	return prefix + keySeparator + secret, nil
}

// prefixOf reads the prefix a presented key carries.
func prefixOf(presented string) string {
	for i, char := range presented {
		if string(char) == keySeparator {
			return presented[:i]
		}
	}
	return presented
}

// descriptionOf turns an absent note into the NULL its column stores.
func descriptionOf(descr string) *string {
	if descr == "" {
		return nil
	}
	return &descr
}
