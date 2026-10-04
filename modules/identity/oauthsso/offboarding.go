package oauthsso

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
)

// defaultOffboardBatch bounds the pass when the caller names no batch —
// the job's own default rides in its task, this is the floor.
const defaultOffboardBatch = 100

// banApplier is the restrictions write the offboard needs: the ban row,
// written inside the caller's transaction. An interface rather than the
// restrictions service, because the oauthsso feature owns the policy —
// what a dead provider identity means — and the restrictions feature owns
// the row's write alone.
type banApplier interface {
	ApplyBanAndLift(ctx context.Context, db datastore.Querier, userID uuid.UUID,
		reason string, expiresAt *time.Time, liftedBy *uuid.UUID, at time.Time) error
}

// sessionEnder is the half of the session lifecycle the offboard needs:
// every live session the banned account holds, ended in the transaction
// the ban is written in. The session service satisfies it structurally —
// the oauthsso package must not import the session package's whole world.
type sessionEnder interface {
	RevokeAllForUser(ctx context.Context, tx datastore.Querier, userID uuid.UUID) (int, error)
}

// OffboardOutcome answers what one pass did: the bindings probed, the
// survivors whose tokens rotated back onto the row, the refused whose
// accounts were offboarded, and the failures skipped.
type OffboardOutcome struct {
	Probed     int
	Rotated    int
	Offboarded int
	Skipped    int
}

// WithBanEnforcer wires the restrictions write the offboard's ban needs.
func (s *Service) WithBanEnforcer(bans banApplier) *Service {
	s.bans = bans
	return s
}

// WithSessionEnder wires the session lifecycle the offboard's revocation
// needs.
func (s *Service) WithSessionEnder(ender sessionEnder) *Service {
	s.ender = ender
	return s
}

// OffboardPass is the offboarding job's probe: it walks the bindings that
// carry a refresh token, presents each to its connection's token endpoint
// through the refresh-token grant, rotates the survivors, and offboards
// the refused — a permanent ban, every session revoked, and one
// `user_banned` audit record, in one transaction. `invalid_grant` is the
// provider's own word for "this identity is gone"; every other failure is
// transport and skips the binding, for this pass and the next to judge.
//
// The pass is bounded: it judges at most `batch` bindings and leaves the
// rest for the next hourly run. Bindings without a refresh token are
// uncheckable and skipped fail-open, named in the log.
func (s *Service) OffboardPass(ctx context.Context, batch int) (OffboardOutcome, error) {
	if batch <= 0 {
		batch = defaultOffboardBatch
	}
	candidates, err := s.repo.OffboardCandidates(ctx, s.pool, batch)
	if err != nil {
		return OffboardOutcome{}, err
	}

	var outcome OffboardOutcome
	for _, candidate := range candidates {
		binding := candidate.LinkedAccount
		conn, connErr := s.repo.ByID(ctx, s.pool, binding.ConnectionID)
		if connErr != nil {
			// The connection the binding names is gone mid-pass; the
			// binding is stranded, not dead — the next pass, or the
			// connection's own lifecycle, owns it.
			outcome.Skipped++
			s.log.WarnContext(ctx, "oauthsso: the offboarding probe skipped a binding whose connection is gone",
				"linked_account_id", binding.ID.String(), "error", connErr.Error())
			continue
		}

		outcome.Probed++
		rotated, refreshErr := s.refreshTokens(ctx, conn, binding.RefreshToken)
		switch {
		case refreshErr == nil:
			if storeErr := s.repo.UpdateBindingTokens(ctx, s.pool, binding.ID,
				rotated.SealedAccessToken, rotated.SealedRefreshToken, rotated.AccessExpiresAt); storeErr != nil {
				return outcome, storeErr
			}
			outcome.Rotated++
		case errors.Is(refreshErr, ErrTokenRejected):
			if offboardErr := s.offboard(ctx, binding, candidate.Provider); offboardErr != nil {
				return outcome, offboardErr
			}
			outcome.Offboarded++
		default:
			outcome.Skipped++
			s.log.WarnContext(ctx, "oauthsso: the offboarding probe could not reach the provider; the binding stays",
				"linked_account_id", binding.ID.String(), "error", refreshErr.Error())
		}
	}

	if tokenless, countErr := s.repo.CountTokenlessBindings(ctx, s.pool); countErr == nil && tokenless > 0 {
		// The fail-open skip is a fact the log keeps, not a silent hole:
		// these bindings cannot be judged, and the pass leaves them.
		s.log.InfoContext(ctx, "oauthsso: the offboarding pass skipped tokenless bindings",
			"count", tokenless)
	}
	return outcome, nil
}

// offboard applies the withdrawal in one transaction: the permanent ban
// row through the restrictions write, the end of every live session the
// account holds, and the `user_banned` audit record that carries the
// offboarding reason — the provider's slug rides inside it. The binding
// row stays: the ban, not the unlink, is the offboard.
func (s *Service) offboard(ctx context.Context, binding LinkedAccount, provider string) error {
	if s.bans == nil {
		return errors.New("oauthsso: the restrictions feature is not wired; the offboard cannot run")
	}
	reason := fmt.Sprintf("offboarded: the oauthsso provider %q rejected the binding's refresh token", provider)
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if banErr := s.bans.ApplyBanAndLift(ctx, tx, binding.UserID, reason, nil, nil, s.now()); banErr != nil {
			return banErr
		}
		ended := 0
		if s.ender != nil {
			var endErr error
			ended, endErr = s.ender.RevokeAllForUser(ctx, tx, binding.UserID)
			if endErr != nil {
				return endErr
			}
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventUserBanned,
			Status: audit.StatusSuccess,
			UserID: binding.UserID.String(),
			Payload: map[string]string{
				"reason":         reason,
				"ended_sessions": fmt.Sprint(ended),
			},
		})
		return nil
	})
}
