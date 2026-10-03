package restrictions

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
)

// The catalog keys the lockout policy reads. The catalog owns the names;
// these constants are how this package spells them.
const (
	SettingLockoutEnabled  = "lockout.enabled"
	SettingLockoutMax      = "lockout.max_attempts"
	SettingLockoutDuration = "lockout.duration"
	settingMaxFloor        = 5
	defaultLockoutMax      = 100
	defaultLockoutDuration = time.Hour
	defaultLockoutEnabled  = true
)

// ErrUnknownAccount is a write whose identifier names no account — the
// caller's not-found failure.
var ErrUnknownAccount = errors.New("restrictions: account not found")

// SettingsReader is the lockout policy's runtime source. *appconfig.Settings
// satisfies it; the interface keeps the appconfig feature out of this one's
// import graph.
type SettingsReader interface {
	GetString(ctx context.Context, key string) (string, error)
	GetBool(ctx context.Context, key string) (bool, error)
}

// NoticeEnqueuer is the locked-account notice's channel: the adapter rides
// the queue after the lockout commits. Nil skips the mail.
type NoticeEnqueuer interface {
	// EnqueueUserLockedNotice tells the account's address the lockout
	// landed, with the window the policy answered.
	EnqueueUserLockedNotice(ctx context.Context, email, displayName string, expiresAt *time.Time)
}

// policy is the lockout settings one call reads, resolved once so the
// branches see one world.
type policy struct {
	enabled  bool
	max      int
	duration time.Duration // zero means the lockout never lifts by itself
}

// Service serves the restriction questions the features ask and writes the
// rows the answers come from. The database writes run on the caller's query
// surface — the causing transaction — so a restriction lands and lifts with
// the fact that caused it.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// audit writes the lock's and the unlock's records, in the transaction
	// that wrote the rows.
	audit *audit.Recorder
	log   *slog.Logger
	now   func() time.Time
	// settings reads the lockout policy at call time. Nil keeps the
	// catalog defaults — the state a test or a bare wiring is in.
	settings SettingsReader
	// notices is the locked-account email's channel. Nil skips the mail;
	// the lockout itself is the policy's, not the seam's.
	notices NoticeEnqueuer
}

// NewService builds the feature over the pool and the shared recorder.
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

// WithSettings wires the lockout policy's runtime source. Nil keeps the
// catalog defaults.
func (s *Service) WithSettings(settings SettingsReader) *Service {
	s.settings = settings
	return s
}

// WithLockedNotice wires the locked-account notice. Nil keeps the policy
// and skips the mail.
func (s *Service) WithLockedNotice(notices NoticeEnqueuer) *Service {
	s.notices = notices
	return s
}

// Status is the active restriction's answer: which kind is in force, why,
// and when it lifts by itself. Kind carries the package's kind constants —
// empty, no restriction stands.
type Status struct {
	Kind      string
	Reason    *string
	ExpiresAt *time.Time
}

// Active answers the account's active restriction — the ban and lockout
// question the sign-in surfaces ask. The read lifts an expired row on its
// way through, so a window that ended stops answering and the streak it
// answered for starts fresh.
func (s *Service) Active(ctx context.Context, db datastore.Querier, userID uuid.UUID) (Status, error) {
	if lifted, err := s.repo.ExpireLifted(ctx, db, userID, s.now()); err != nil {
		return Status{}, err
	} else if lifted {
		// The window ended: the streak it answered for starts fresh.
		return Status{}, s.ResetStreak(ctx, db, userID)
	}

	row, err := s.repo.ActiveRow(ctx, db, userID, s.now())
	if err != nil {
		return Status{}, err
	}
	if row == nil {
		return Status{}, nil
	}
	return Status{Kind: row.Kind, Reason: row.Reason, ExpiresAt: row.ExpiresAt}, nil
}

// policyOf reads the catalog. The bare wiring answers the catalog defaults
// — enabled, one hundred attempts, a one-hour window — and an unreadable
// key keeps its default, so a settings row that cannot answer never
// rewrites the policy silently.
func (s *Service) policyOf(ctx context.Context) policy {
	p := policy{enabled: defaultLockoutEnabled, max: defaultLockoutMax, duration: defaultLockoutDuration}
	if s.settings == nil {
		return p
	}
	if on, err := s.settings.GetBool(ctx, SettingLockoutEnabled); err != nil {
		s.log.WarnContext(ctx, "restrictions: lockout.enabled unreadable; using the default", "error", err)
	} else {
		p.enabled = on
	}
	if raw, err := s.settings.GetString(ctx, SettingLockoutMax); err != nil {
		s.log.WarnContext(ctx, "restrictions: lockout.max_attempts unreadable; using the default", "error", err)
	} else if parsed, parseErr := strconv.Atoi(raw); parseErr != nil || parsed < settingMaxFloor {
		s.log.WarnContext(ctx, "restrictions: lockout.max_attempts is out of the reader's floor; using the default")
	} else {
		p.max = parsed
	}
	if raw, err := s.settings.GetString(ctx, SettingLockoutDuration); err != nil {
		s.log.WarnContext(ctx, "restrictions: lockout.duration unreadable; using the default", "error", err)
	} else if strings.TrimSpace(raw) == "" {
		p.duration = 0
	} else if parsed, parseErr := time.ParseDuration(raw); parseErr != nil {
		s.log.WarnContext(ctx, "restrictions: lockout.duration does not parse; using the default", "error", parseErr)
	} else {
		p.duration = parsed
	}
	return p
}

// RegisterFailure advances the account's failed-password streak and lands
// the lockout when the streak reaches the policy's bound. It runs on the
// caller's transaction — the same one that judged the credential — so the
// count, the lockout row, and the audit record commit with the facts that
// caused them. A streak under the bound changes nothing but the count; the
// answer says whether the lockout landed.
//
// The policy off, nothing is counted — the deployment refused the feature,
// not the attempt.
func (s *Service) RegisterFailure(ctx context.Context, userID uuid.UUID, email, displayName string) (bool, error) {
	p := s.policyOf(ctx)
	if !p.enabled {
		return false, nil
	}

	var locked bool
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		attempts, err := s.repo.BumpFailures(ctx, tx, userID)
		if errors.Is(err, datastore.ErrNoRows) {
			return ErrUnknownAccount
		}
		if err != nil {
			return err
		}
		if attempts < p.max {
			return nil
		}

		var expiresAt *time.Time
		if p.duration > 0 {
			at := s.now().Add(p.duration)
			expiresAt = &at
		}
		if err := s.repo.ApplyLockout(ctx, tx, userID, expiresAt, s.now()); err != nil {
			return err
		}
		payload := map[string]string{"attempts": strconv.Itoa(attempts)}
		if expiresAt != nil {
			payload["expires_at"] = expiresAt.Format(time.RFC3339)
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:   audit.EventUserLocked,
			Status:  audit.StatusSuccess,
			UserID:  userID.String(),
			Payload: payload,
		})
		if s.notices != nil {
			s.notices.EnqueueUserLockedNotice(ctx, email, displayName, expiresAt)
		}
		locked = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return locked, nil
}

// ResetStreak clears the account's failed-password streak — the write a
// successful verification makes, so the streak never outlives the sign-in
// run it belongs to.
func (s *Service) ResetStreak(ctx context.Context, db datastore.Querier, userID uuid.UUID) error {
	return s.repo.ZeroFailures(ctx, db, userID)
}

// Unlock lifts the account's open lockout and zeroes the streak it
// answered for, in one transaction with the audit record. The administrator
// actor's identifier rides the record; the lockout's own expiry answers for
// itself without one. An account with no open lockout is the same success —
// the state the caller asked for is the state the account is in.
func (s *Service) Unlock(ctx context.Context, userID uuid.UUID, actorID *uuid.UUID) (bool, error) {
	var lifted bool
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		done, err := s.repo.LiftLockouts(ctx, tx, userID, actorID, s.now())
		if err != nil {
			return err
		}
		lifted = done
		if !done {
			return nil
		}
		payload := map[string]string{}
		if actorID != nil {
			payload["actor"] = actorID.String()
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:   audit.EventUserUnlocked,
			Status:  audit.StatusSuccess,
			UserID:  userID.String(),
			Payload: payload,
		})
		return s.ResetStreak(ctx, tx, userID)
	})
	if err != nil {
		return false, err
	}
	return lifted, nil
}

// ApplyBanAndLift writes the ban the terms name inside the caller's
// transaction — the user feature's ban orchestration owns the sessions, the
// notice, and the audit; this is the row's write alone. LiftedBy records
// the administrator a lift names, and at is the instant the caller's own
// clock answers — the write carries the clock of the flow that caused it.
func (s *Service) ApplyBanAndLift(ctx context.Context, db datastore.Querier, userID uuid.UUID, reason string, expiresAt *time.Time, liftedBy *uuid.UUID, at time.Time) error {
	if err := s.repo.ApplyBan(ctx, db, userID, reason, expiresAt, at); err != nil {
		return err
	}
	// A previous open lockout the ban supersedes lifts with it: the
	// account is restricted, the lockout's window has nothing left to
	// protect.
	if _, err := s.repo.LiftLockouts(ctx, db, userID, liftedBy, at); err != nil {
		return err
	}
	return nil
}

// LiftBans stamps the end of the account's open ban rows — the user
// feature's unban orchestration owns the audit and the notice; this is the
// row's write alone.
func (s *Service) LiftBans(ctx context.Context, db datastore.Querier, userID uuid.UUID, liftedBy *uuid.UUID, at time.Time) error {
	_, err := s.repo.LiftBans(ctx, db, userID, liftedBy, at)
	return err
}
