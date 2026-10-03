package user

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
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
	if s.restrictions == nil {
		return BanOutcome{}, ErrUserNotFound
	}

	// The ban is a restriction row now: an open row's reason and expiry
	// are replaced — the write is the caller's intent, not a comparison —
	// and the start instant answers "since when", so a re-ban does not
	// move it. A lockout the ban supersedes lifts with it.
	var ended int
	var banView UserView
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if banErr := s.restrictions.ApplyBanAndLift(ctx, tx, userID, params.Reason, params.ExpiresAt, nil, s.now()); banErr != nil {
			return banErr
		}
		if s.sessions != nil {
			var sessionErr error
			ended, sessionErr = s.sessions.RevokeAllForUser(ctx, tx, userID)
			if sessionErr != nil {
				return sessionErr
			}
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventUserBanned,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"username":       existing.Username,
				"reason":         params.Reason,
				"ended_sessions": fmt.Sprint(ended),
			},
		})
		// The read-back answers the view the wire carries, ban fields
		// included: the join sees the row this transaction wrote.
		read, readErr := s.repo.GetUser(ctx, tx, userID)
		if readErr != nil {
			return readErr
		}
		banView = view(read)
		return nil
	})
	if err != nil {
		return BanOutcome{}, err
	}

	if s.notify != nil {
		s.notify.UserBanned(ctx, existing.Email, banView, params.ExpiresAt)
	}
	filled, fillErr := s.withGroup(ctx, s.pool, banView)
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
	if s.restrictions == nil {
		return BanOutcome{}, ErrUserNotFound
	}

	var unbanView UserView
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if liftErr := s.restrictions.LiftBans(ctx, tx, userID, nil, s.now()); liftErr != nil {
			return liftErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventUserUnbanned,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"username": existing.Username,
			},
		})
		read, readErr := s.repo.GetUser(ctx, tx, userID)
		if readErr != nil {
			return readErr
		}
		unbanView = view(read)
		return nil
	})
	if err != nil {
		return BanOutcome{}, err
	}

	if s.notify != nil {
		s.notify.UserUnbanned(ctx, existing.Email, unbanView)
	}
	filled, fillErr := s.withGroup(ctx, s.pool, unbanView)
	if fillErr != nil {
		return BanOutcome{}, fillErr
	}
	return BanOutcome{User: filled}, nil
}

// UnlockUser lifts the account's open lockout — the automated restriction
// the failed-attempt policy writes — and zeroes the streak it answered for.
// An account with no open lockout is the same success. A ban is not a
// lockout and the procedure does not touch one; UnbanUser owns that.
func (s *Service) UnlockUser(ctx context.Context, id string) (UserView, error) {
	userID, err := parseWire(id)
	if err != nil {
		return UserView{}, ErrUserNotFound
	}
	if s.restrictions == nil {
		return UserView{}, ErrUserNotFound
	}

	existing, err := s.repo.GetUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return UserView{}, ErrUserNotFound
	}
	if err != nil {
		return UserView{}, err
	}

	if _, err := s.restrictions.Unlock(ctx, userID, nil); err != nil {
		return UserView{}, err
	}
	return view(existing), nil
}
