package customclaim

import (
	"context"
	"errors"
	"log/slog"
	"time"
	"uuid"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/modules/identity/usergroup"
)

// Service carries the rules of the custom claims: which subject kinds carry
// which procedures, what a duplicate key answers, and what a change records.
// The repository carries the SQL.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// audit writes the record of every claim change, in the transaction
	// that makes the change.
	audit *audit.Recorder
	log   *slog.Logger
	now   func() time.Time
}

// NewService builds the service. The database writes run in one transaction
// the service opens over the pool, so a claim and its audit record commit
// together or not at all.
func NewService(pool *datastore.Postgres, recorder *audit.Recorder, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		pool:  pool,
		repo:  NewRepository(),
		audit: recorder,
		log:   log,
		now:   time.Now,
	}
}

// Suggest answers the claim keys already in use, most-used first.
func (s *Service) Suggest(ctx context.Context) ([]SuggestedKey, error) {
	return s.repo.Suggest(ctx, s.pool)
}

// UserClaims answers the claims one account carries — the token-issuance
// seam's read, returning the key/value pairs the tokens carry rather than
// the rows' identifiers.
func (s *Service) UserClaims(ctx context.Context, userID uuid.UUID) ([]Claim, error) {
	rows, err := s.repo.ListByUser(ctx, s.pool, userID)
	if err != nil {
		return nil, err
	}
	claims := make([]Claim, 0, len(rows))
	for _, row := range rows {
		claims = append(claims, Claim{Key: row.Key, Value: row.Value})
	}
	return claims, nil
}

// GroupClaims answers the claims the named groups carry, merged in one
// read — the group side of the same seam.
func (s *Service) GroupClaims(ctx context.Context, groupIDs []uuid.UUID) ([]Claim, error) {
	rows, err := s.repo.ListByGroups(ctx, s.pool, groupIDs)
	if err != nil {
		return nil, err
	}
	claims := make([]Claim, 0, len(rows))
	for _, row := range rows {
		claims = append(claims, Claim{Key: row.Key, Value: row.Value})
	}
	return claims, nil
}

// ListByUser answers one account's claims.
func (s *Service) ListByUser(ctx context.Context, wireUserID string) ([]ClaimView, error) {
	userID, err := userUUID(wireUserID)
	if err != nil {
		return nil, ErrSubjectNotFound
	}
	rows, err := s.repo.ListByUser(ctx, s.pool, userID)
	if err != nil {
		return nil, err
	}
	return views(rows), nil
}

// ListByGroup answers one group's claims.
func (s *Service) ListByGroup(ctx context.Context, wireGroupID string) ([]ClaimView, error) {
	groupID, err := groupUUID(wireGroupID)
	if err != nil {
		return nil, ErrSubjectNotFound
	}
	rows, err := s.repo.ListByGroup(ctx, s.pool, groupID)
	if err != nil {
		return nil, err
	}
	return views(rows), nil
}

// CreateByUser hangs a claim on an account.
func (s *Service) CreateByUser(ctx context.Context, wireUserID, key, value string) (ClaimView, error) {
	if IsReservedClaimKey(key) {
		return ClaimView{}, ErrReservedClaim
	}
	userID, err := userUUID(wireUserID)
	if err != nil {
		return ClaimView{}, ErrSubjectNotFound
	}
	var created ClaimSchema
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, rowErr := s.repo.CreateUser(ctx, tx, userID, key, value, s.now())
		if rowErr != nil {
			return mapWriteError(rowErr)
		}
		created = row
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventCustomClaimCreated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceCustomClaim,
			ResourceID:   row.ID.String(),
			Payload:      map[string]string{"subject": "user", "key": key},
		})
		return nil
	})
	if err != nil {
		return ClaimView{}, err
	}
	return created.view(), nil
}

// CreateByGroup hangs a claim on a group.
func (s *Service) CreateByGroup(ctx context.Context, wireGroupID, key, value string) (ClaimView, error) {
	if IsReservedClaimKey(key) {
		return ClaimView{}, ErrReservedClaim
	}
	groupID, err := groupUUID(wireGroupID)
	if err != nil {
		return ClaimView{}, ErrSubjectNotFound
	}
	var created ClaimSchema
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, rowErr := s.repo.CreateGroup(ctx, tx, groupID, key, value, s.now())
		if rowErr != nil {
			return mapWriteError(rowErr)
		}
		created = row
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventCustomClaimCreated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceCustomClaim,
			ResourceID:   row.ID.String(),
			Payload:      map[string]string{"subject": "user_group", "key": key},
		})
		return nil
	})
	if err != nil {
		return ClaimView{}, err
	}
	return created.view(), nil
}

// UpdateByUser rewrites one of an account's claims. The subject kind is the
// row's own: a group claim offered to the user surface is refused, so a
// client cannot silently move a claim between subjects by pointing the
// other surface at it.
func (s *Service) UpdateByUser(ctx context.Context, wireClaimID, key, value string) (ClaimView, error) {
	return s.update(ctx, wireClaimID, key, value, subjectUser)
}

// UpdateByGroup rewrites one of a group's claims.
func (s *Service) UpdateByGroup(ctx context.Context, wireClaimID, key, value string) (ClaimView, error) {
	return s.update(ctx, wireClaimID, key, value, subjectGroup)
}

// DeleteByUser removes one of an account's claims; a group claim is refused.
func (s *Service) DeleteByUser(ctx context.Context, wireClaimID string) error {
	return s.delete(ctx, wireClaimID, subjectUser)
}

// DeleteByGroup removes one of a group's claims; a user claim is refused.
func (s *Service) DeleteByGroup(ctx context.Context, wireClaimID string) error {
	return s.delete(ctx, wireClaimID, subjectGroup)
}

// The two subject kinds the table's check constraint allows, and the
// predicates each surface's procedures demand of a row.
type subjectKind int

const (
	subjectUser subjectKind = iota
	subjectGroup
)

// holds reports whether the row belongs to the subject kind the procedure
// is written for.
func (k subjectKind) holds(row ClaimSchema) bool {
	if k == subjectUser {
		return row.UserID != nil
	}
	return row.GroupID != nil
}

func (s *Service) update(ctx context.Context, wireClaimID, key, value string, kind subjectKind) (ClaimView, error) {
	if IsReservedClaimKey(key) {
		return ClaimView{}, ErrReservedClaim
	}
	claimID, err := parseClaimID(wireClaimID)
	if err != nil {
		return ClaimView{}, ErrClaimNotFound
	}

	var updated ClaimView
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, rowErr := s.repo.GetClaim(ctx, tx, claimID)
		if errors.Is(rowErr, datastore.ErrNoRows) {
			return ErrClaimNotFound
		}
		if rowErr != nil {
			return rowErr
		}
		if !kind.holds(row) {
			return ErrClaimWrongSubject
		}

		row, changed, updateErr := s.repo.UpdateClaim(ctx, tx, claimID, key, value)
		if updateErr != nil {
			return mapWriteError(updateErr)
		}
		if !changed {
			// A concurrent deletion between the read and the write: the
			// claim the surface names is gone, the not-found it answers.
			return ErrClaimNotFound
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventCustomClaimUpdated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceCustomClaim,
			ResourceID:   claimID.String(),
			Payload:      map[string]string{"key": key},
		})
		updated = row.view()
		return nil
	})
	if err != nil {
		return ClaimView{}, err
	}
	return updated, nil
}

func (s *Service) delete(ctx context.Context, wireClaimID string, kind subjectKind) error {
	claimID, err := parseClaimID(wireClaimID)
	if err != nil {
		return ErrClaimNotFound
	}

	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, err := s.repo.GetClaim(ctx, tx, claimID)
		if errors.Is(err, datastore.ErrNoRows) {
			return ErrClaimNotFound
		}
		if err != nil {
			return err
		}
		if !kind.holds(row) {
			return ErrClaimWrongSubject
		}

		deleted, err := s.repo.DeleteClaim(ctx, tx, claimID)
		if err != nil {
			return err
		}
		if !deleted {
			return ErrClaimNotFound
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventCustomClaimDeleted,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceCustomClaim,
			ResourceID:   claimID.String(),
			Payload:      map[string]string{"key": row.Key},
		})
		return nil
	})
}

// views maps a page of rows.
func views(rows []ClaimSchema) []ClaimView {
	list := make([]ClaimView, 0, len(rows))
	for _, row := range rows {
		list = append(list, row.view())
	}
	return list
}

// mapWriteError reads a write's failure: the unique index answers a
// duplicate key on the subject, the foreign keys answer an unknown one.
func mapWriteError(err error) error {
	switch {
	case errUniqueViolation(err):
		return ErrClaimExists
	case errForeignKeyViolation(err):
		return ErrSubjectNotFound
	default:
		return err
	}
}

// userUUID is the account boundary: the wire form a request carries in, the
// key the rows carry out. A malformed identifier names no account.
func userUUID(wire string) (uuid.UUID, error) {
	id, err := user.UUIDFromWire(wire)
	if err != nil {
		return uuid.Nil(), err
	}
	return id, nil
}

// groupUUID is the group boundary, the same shape the account boundary is.
func groupUUID(wire string) (uuid.UUID, error) {
	id, err := usergroup.UUIDFromWire(wire)
	if err != nil {
		return uuid.Nil(), err
	}
	return id, nil
}
