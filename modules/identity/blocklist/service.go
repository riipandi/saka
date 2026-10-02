package blocklist

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/pkg/responder"
)

// The failures the service defines. The handler maps them onto the codes the
// Connect protocol carries; the service defines what happened, not how it is
// answered.
var (
	// ErrEntryNotFound is an identifier that names no entry.
	ErrEntryNotFound = errors.New("blocklist: entry not found")

	// ErrPatternInvalid is an entry the grammar refuses — not an address,
	// not a domain, or a shape the list does not carry (a wildcard has no
	// meaning here).
	ErrPatternInvalid = errors.New("blocklist: entry is not an email address or an @domain entry")
)

// Service carries the rules of the blocklist: how an entry is stored, what a
// change records, and whether an address is refused. The repository carries
// the SQL.
type Service struct {
	pool  *datastore.Postgres
	repo  *Repository
	audit *audit.Recorder
	log   *slog.Logger
	now   func() time.Time
}

// NewService builds the service over the shared pool.
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

// List answers one page of the entries, ordered as the caller asked (absent
// a choice, newest first).
func (s *Service) List(ctx context.Context, sortBy string, ascending bool, page, limit int) ([]EntrySchema, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)
	entries, total, err := s.repo.ListEntries(ctx, s.pool, sortBy, ascending, responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	return entries, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
}

// Add stores one entry. The pattern is validated against the grammar and
// stored normalized; an entry the list already carries answers the stored
// row, so the caller sees the identifier's real state. The audit record
// commits in the transaction that inserts — and only when a row is inserted,
// so re-adding a live entry does not re-report it.
func (s *Service) Add(ctx context.Context, adminID uuid.UUID, pattern string) (EntrySchema, error) {
	normalized, err := ValidatePattern(pattern)
	if err != nil {
		return EntrySchema{}, err
	}

	var entry EntrySchema
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		admin := adminID
		inserted, insertErr := s.repo.InsertEntry(ctx, tx, EntrySchema{
			ID:        uuid.NewV7(),
			Pattern:   normalized,
			CreatedBy: &admin,
		})
		if insertErr != nil {
			return insertErr
		}

		entry, err = s.repo.GetEntryByPattern(ctx, tx, normalized)
		if err != nil {
			return err
		}

		if inserted {
			s.audit.Record(ctx, tx, audit.Entry{
				Event:        audit.EventBlocklistEntryAdded,
				Status:       audit.StatusSuccess,
				UserID:       adminID.String(),
				ResourceType: ResourceBlocklistEntry,
				ResourceID:   entry.ID.String(),
				Payload: map[string]string{
					"pattern": normalized,
				},
			})
		}
		return nil
	})
	if err != nil {
		return EntrySchema{}, err
	}
	return entry, nil
}

// Remove deletes one entry by identifier. The row is read first, so the
// record names the identifier that left; the deletion and the record of it
// commit together.
func (s *Service) Remove(ctx context.Context, id uuid.UUID) error {
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		entry, err := s.repo.GetEntry(ctx, tx, id)
		if errors.Is(err, datastore.ErrNoRows) {
			return ErrEntryNotFound
		}
		if err != nil {
			return err
		}
		deleted, err := s.repo.DeleteEntry(ctx, tx, id)
		if err != nil {
			return err
		}
		if !deleted {
			return ErrEntryNotFound
		}

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventBlocklistEntryRemoved,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceBlocklistEntry,
			ResourceID:   id.String(),
			Payload: map[string]string{
				"pattern": entry.Pattern,
			},
		})
		return nil
	})
}

// Blocked answers whether the address is refused for the stored entries —
// the question the sign-up and sign-in gates ask. A failed read is answered
// false with a warn: the lists fail open, the way an unreadable setting
// keeps its default, because a broken read must never lock a deployment out
// of its own sign-up.
func (s *Service) Blocked(ctx context.Context, address string) (bool, error) {
	patterns, err := s.repo.Patterns(ctx, s.pool)
	if err != nil {
		return false, fmt.Errorf("blocklist: read for the gate: %w", err)
	}
	return Matches(address, patterns), nil
}

// CollisionTaken answers whether an account already holds an address whose
// collision base equals the candidate's — the question the
// block-email-subaddresses gate asks at sign-up and at an email change. The
// candidate's own exact duplicate is not a collision: the caller's unique
// index or taken-address check answers that with the failure the contract
// carries, so the scan skips it and compares the folded bases.
func (s *Service) CollisionTaken(ctx context.Context, address string) (bool, error) {
	normalized := strings.ToLower(strings.TrimSpace(address))
	base := CollisionBase(normalized)
	_, domain, ok := strings.Cut(base, "@")
	if !ok || domain == "" {
		return false, nil
	}

	emails, err := s.repo.EmailsAtDomain(ctx, s.pool, domain)
	if err != nil {
		return false, err
	}
	for _, email := range emails {
		if email == normalized {
			continue
		}
		if CollisionBase(email) == base {
			return true, nil
		}
	}
	return false, nil
}
