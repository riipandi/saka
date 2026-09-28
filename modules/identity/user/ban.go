package user

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
)

// BanParams carries the terms of a ban: the reason the audit trail and the
// notification both keep, and the expiry — nil for a ban that never lifts
// by itself.
type BanParams struct {
	Reason    string
	ExpiresAt *time.Time
}

// BanOutcome answers the write and its blast radius: the account as the ban
// left it, and how many live sessions the ban ended.
type BanOutcome struct {
	User          UserView
	EndedSessions int
}

// ErrBanInPast is an expiry the clock has already passed: it would write a
// ban that is over the moment it is written, a state the caller cannot have
// meant.
var ErrBanInPast = errors.New("user: ban expiry is in the past")

// sessionEnder is the half of the session lifecycle a ban needs: the rows it
// ends, in the transaction the ban is written in. An interface rather than
// the session service, because the user package must not import the session
// package's whole world — the ban owns the policy, the lifecycle owns the
// rows.
type sessionEnder interface {
	RevokeAllForUser(ctx context.Context, tx datastore.Querier, userID uuid.UUID) (int, error)
}

// BanUser applies the ban in one transaction: the row's ban fields, the
// end of every live session the account holds, and the audit record that
// names the reason and the count of ended sessions. The notification is
// queued after the transaction commits — the queue is durable on its own,
// and a message the ban rolled back must not exist.
//
// The rules the contract carries and the ones it cannot: the identifier
// must name an account (the not-found failure), the expiry must be in the
// future, and an already-banned account is the same success — the write
// replaces the reason and the expiry, which is what re-issuing a term is.
func (s *Service) BanUser(ctx context.Context, id string, params BanParams) (BanOutcome, error) {
	userID, err := parseWire(id)
	if err != nil {
		return BanOutcome{}, ErrUserNotFound
	}
	now := s.now()
	if params.ExpiresAt != nil && !params.ExpiresAt.After(now) {
		return BanOutcome{}, ErrBanInPast
	}

	existing, err := s.repo.GetUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return BanOutcome{}, ErrUserNotFound
	}
	if err != nil {
		return BanOutcome{}, err
	}

	// The ban is applied now unless an earlier one is on record: the start
	// instant answers "since when", so a re-ban does not move it.
	bannedAt := now
	if existing.BannedAt != nil {
		bannedAt = *existing.BannedAt
	}
	row := existing
	row.BannedAt = &bannedAt
	row.BanExpires = params.ExpiresAt
	row.BanReason = &params.Reason

	var ended int
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		updated, updateErr := s.repo.UpdateUser(ctx, tx, row)
		if updateErr != nil {
			return updateErr
		}
		if !updated {
			return ErrUserNotFound
		}
		if s.sessions != nil {
			ended, updateErr = s.sessions.RevokeAllForUser(ctx, tx, userID)
			if updateErr != nil {
				return updateErr
			}
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventUserBanned,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"username":       row.Username,
				"reason":         params.Reason,
				"ended_sessions": fmt.Sprint(ended),
			},
		})
		return nil
	})
	if err != nil {
		return BanOutcome{}, err
	}

	if s.notify != nil {
		s.notify.UserBanned(ctx, row.Email, view(row), params.ExpiresAt)
	}
	filled, fillErr := s.withGroup(ctx, s.pool, view(row))
	if fillErr != nil {
		return BanOutcome{}, fillErr
	}
	return BanOutcome{User: filled, EndedSessions: ended}, nil
}

// UnbanUser lifts the ban as a unit — start instant, expiry, and reason
// together — in one transaction with its audit record. An account that is
// not banned is the same success: the state the caller asked for is the
// state the row is in, and the answer carries it without a write.
func (s *Service) UnbanUser(ctx context.Context, id string) (BanOutcome, error) {
	userID, err := parseWire(id)
	if err != nil {
		return BanOutcome{}, ErrUserNotFound
	}

	existing, err := s.repo.GetUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return BanOutcome{}, ErrUserNotFound
	}
	if err != nil {
		return BanOutcome{}, err
	}

	row := existing
	row.BannedAt = nil
	row.BanExpires = nil
	row.BanReason = nil

	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		updated, updateErr := s.repo.UpdateUser(ctx, tx, row)
		if updateErr != nil {
			return updateErr
		}
		if !updated {
			return ErrUserNotFound
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventUserUnbanned,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"username": row.Username,
			},
		})
		return nil
	})
	if err != nil {
		return BanOutcome{}, err
	}

	if s.notify != nil {
		s.notify.UserUnbanned(ctx, row.Email, view(row))
	}
	filled, fillErr := s.withGroup(ctx, s.pool, view(row))
	if fillErr != nil {
		return BanOutcome{}, fillErr
	}
	return BanOutcome{User: filled}, nil
}
