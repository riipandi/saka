package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.jetify.com/typeid"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	appauthz "github.com/riipandi/saka/internal/authz"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// ImpersonationTTL is the fixed window a delegated session lives. It does
// not follow the target's normal lifetimes — remember or not — because the
// window's purpose is different: it bounds how long a borrowed identity can
// act, not how long an owner wants to stay signed in. Better Auth's default
// (one hour) is the reference; the constant keeps it from drifting with the
// config keys that answer a different question.
const ImpersonationTTL = time.Hour

// ImpersonationProvider is the `provider` value a delegated session row
// carries. The column's vocabulary is how a session was opened, and a
// delegation is its own way of opening one.
const ImpersonationProvider = "impersonation"

// ErrTargetNotFound is an impersonation target no account answers. It is the
// not-found failure — the same answer a malformed identifier earns, so the
// endpoint does not disclose which half was wrong.
var ErrTargetNotFound = errors.New("session: impersonation target not found")

// ErrTargetAdmin is an impersonation target that holds administrative
// privileges itself. An administrator wearing another administrator's
// identity would defeat the audit trail's ability to name who acted.
var ErrTargetAdmin = errors.New("session: an administrator may not be impersonated")

// ErrTargetSelf is an impersonation request whose target is the caller: the
// delegation would record a fiction, so it refuses with the mistake named.
var ErrTargetSelf = errors.New("session: an account may not impersonate itself")

// ErrNotImpersonating is a stop request from a caller whose token carries no
// delegation. The pair the caller holds is their own; there is nothing to
// step out of.
var ErrNotImpersonating = errors.New("session: the caller is not impersonating")

// ImpersonateUser opens a new session on the target account's behalf. The
// subject names the target, the actor pair names the administrator, and the
// row records the delegation in `impersonated_by`. The rules in full:
//
//   - the target must exist, must not be disabled or banned (the issuer's
//     check — a delegation is a session, and a session of a closed account
//     must not open), and must not hold administrative privileges;
//   - the caller may not impersonate themselves — the delegation would be
//     indistinguishable from their own session while recording a fiction;
//   - the window is ImpersonationTTL, fixed, and the refresh rotation keeps
//     the delegation alive inside it — the actor pair rides every renewal
//     because the claims are re-signed from the row's bookkeeping, never
//     from the original token.
//
// The audit record is written in the same transaction the session row is:
// a delegation that rolled back must not appear to have happened.
func (s *Service) ImpersonateUser(ctx context.Context, callerID, callerUsername, targetWire, reason string) (Refreshed, error) {
	client := audit.ClientFromContext(ctx)
	targetID, err := user.UUIDFromWire(targetWire)
	if err != nil {
		return Refreshed{}, ErrTargetNotFound
	}
	callerUUID, err := user.UUIDFromWire(callerID)
	if err != nil {
		return Refreshed{}, ErrSessionEnded
	}
	if targetID == callerUUID {
		// The caller's own account is not a delegation, it is a rename: the
		// honest refusal names the mistake instead of accusing the target.
		return Refreshed{}, ErrTargetSelf
	}

	now := s.now()
	view, err := user.ReadAccount(ctx, s.pool, targetID)
	if errors.Is(err, datastore.ErrNoRows) {
		return Refreshed{}, ErrTargetNotFound
	}
	if err != nil {
		return Refreshed{}, err
	}
	targetRoles, targetPermissions, grantsErr := user.LoadGrants(ctx, s.pool, targetID)
	if grantsErr != nil {
		return Refreshed{}, grantsErr
	}
	if slices.Contains(targetRoles, appauthz.AdministratorRole) {
		return Refreshed{}, ErrTargetAdmin
	}
	if view.Disabled || bannedAt(view, now) {
		// The same refusal an account in this state answers at sign-in: the
		// target's standing is disclosed to the administrator who asked, in
		// the word the sign-in issuer uses.
		return Refreshed{}, ErrSessionEnded
	}

	refresh, err := crypto.NewRefreshTokenPair()
	if err != nil {
		return Refreshed{}, fmt.Errorf("session: refresh token: %w", err)
	}

	sessionID, err := typeid.New[SessionID]()
	if err != nil {
		return Refreshed{}, fmt.Errorf("session: session id: %w", err)
	}
	expires := now.Add(ImpersonationTTL)
	row := SessionSchema{
		ID:                sessionID,
		UserID:            targetID,
		Provider:          ImpersonationProvider,
		TokenHash:         refresh.Hash,
		UserAgent:         client.UserAgent,
		DeviceFingerprint: client.Fingerprint,
		IPAddress:         addrPtr(client.IPAddress),
		Remember:          false,
		CreatedAt:         now,
		ExpiresAt:         expires,
		ImpersonatedBy:    &callerUUID,
	}

	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if createErr := s.repo.Create(ctx, tx, row); createErr != nil {
			return createErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventImpersonationStarted,
			Status:       audit.StatusSuccess,
			UserID:       targetID.String(),
			ResourceType: ResourceSession,
			ResourceID:   sessionID.UUID(),
			Payload: map[string]string{
				"actor_id":       user.FormatID(callerUUID),
				"actor_username": callerUsername,
				"reason":         reason,
				"provider":       ImpersonationProvider,
			},
		})
		return nil
	})
	if err != nil {
		return Refreshed{}, err
	}

	access, err := s.issuer.SignSessionToken(ctx, view.ID, jwtutils.AccessClaims{
		Email:         view.Email,
		Username:      view.Username,
		DisplayName:   view.DisplayName,
		Roles:         targetRoles,
		Permissions:   targetPermissions,
		SessionID:     sessionID.String(),
		ActorID:       user.FormatID(callerUUID),
		ActorUsername: callerUsername,
	}, sessionID, now)
	if err != nil {
		return Refreshed{}, err
	}

	return Refreshed{
		AccessToken:      access,
		TokenType:        jwtutils.BearerScheme,
		AccessExpiresIn:  int32(s.issuer.AccessTokenTTL().Seconds()),
		RefreshExpiresIn: int32(ImpersonationTTL.Seconds()),
		RefreshToken:     refresh.Plain,
		SessionID:        sessionID.String(),
		User:             view,
	}, nil
}

// StopImpersonating ends the delegated session the caller rides in and opens
// a fresh session on the administrator's own account. The caller's actor
// pair is the proof of who to re-issue to: the delegation's row names them
// in `impersonated_by`, and the claims named them before the swap — the
// service refuses a stop request whose session is not a delegation, because
// ending a session an administrator owns outright is SignOut's work.
//
// The new session is a fresh sign-in from the same request facts, not a
// restoration of some prior session: the original's refresh token is long
// gone or rotated, and re-issuing from the actor identity is the one answer
// that cannot resurrect state the caller never held. The record is written
// in the transaction that ends the delegation, with the user_id naming the
// target so the started/stopped pair reads together.
func (s *Service) StopImpersonating(ctx context.Context, callerSession string, caller *jwtutils.Caller) (Refreshed, error) {
	client := audit.ClientFromContext(ctx)
	if caller == nil || caller.ActorID == "" {
		return Refreshed{}, ErrNotImpersonating
	}
	actorID, err := user.UUIDFromWire(caller.ActorID)
	if err != nil {
		return Refreshed{}, ErrNotImpersonating
	}

	sid, err := parseSessionID(callerSession)
	if err != nil {
		return Refreshed{}, ErrSessionEnded
	}
	now := s.now()

	// The delegation's row is read outside the write transaction: the
	// impersonated_by column is the durable fact the actor claims are
	// checked against, so a forged actor pair on a live non-delegated
	// session finds nothing here.
	row, err := s.repo.GetSession(ctx, s.pool, sid)
	if errors.Is(err, datastore.ErrNoRows) {
		return Refreshed{}, ErrSessionEnded
	}
	if err != nil {
		return Refreshed{}, err
	}
	if row.ImpersonatedBy == nil || *row.ImpersonatedBy != actorID {
		return Refreshed{}, ErrNotImpersonating
	}

	view, err := user.ReadAccount(ctx, s.pool, actorID)
	if errors.Is(err, datastore.ErrNoRows) {
		// The administrator's account was deleted while their delegation
		// lived: the delegation ends, and there is nothing to re-issue to.
		_, revokeErr := s.repo.Revoke(ctx, s.pool, sid, &actorID, now)
		if revokeErr != nil {
			return Refreshed{}, revokeErr
		}
		return Refreshed{}, ErrSessionEnded
	}
	if err != nil {
		return Refreshed{}, err
	}
	if view.Disabled || bannedAt(view, now) {
		return Refreshed{}, ErrSessionEnded
	}
	actorRoles, actorPermissions, grantsErr := user.LoadGrants(ctx, s.pool, actorID)
	if grantsErr != nil {
		return Refreshed{}, grantsErr
	}

	refresh, err := crypto.NewRefreshTokenPair()
	if err != nil {
		return Refreshed{}, fmt.Errorf("session: refresh token: %w", err)
	}
	newID, err := typeid.New[SessionID]()
	if err != nil {
		return Refreshed{}, fmt.Errorf("session: session id: %w", err)
	}

	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if _, revokeErr := s.repo.Revoke(ctx, tx, sid, &actorID, now); revokeErr != nil {
			return revokeErr
		}
		if createErr := s.repo.Create(ctx, tx, SessionSchema{
			ID:                newID,
			UserID:            actorID,
			Provider:          ImpersonationProvider,
			TokenHash:         refresh.Hash,
			UserAgent:         client.UserAgent,
			DeviceFingerprint: client.Fingerprint,
			IPAddress:         addrPtr(client.IPAddress),
			Remember:          false,
			CreatedAt:         now,
			ExpiresAt:         now.Add(s.issuer.SessionLifetime(ctx, false)),
		}); createErr != nil {
			return createErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventImpersonationStopped,
			Status:       audit.StatusSuccess,
			UserID:       row.UserID.String(),
			ResourceType: ResourceSession,
			ResourceID:   sid.UUID(),
			Payload: map[string]string{
				"actor_id": user.FormatID(actorID),
				// The username comes from the account's row, not the claims:
				// the renewal that preceded the stop does not carry the
				// pair's second half, and the row is the fact that survives.
				"actor_username": view.Username,
				"provider":       ImpersonationProvider,
			},
		})
		return nil
	})
	if err != nil {
		return Refreshed{}, err
	}

	access, err := s.issuer.SignSessionToken(ctx, view.ID, jwtutils.AccessClaims{
		Email:       view.Email,
		Username:    view.Username,
		DisplayName: view.DisplayName,
		Roles:       actorRoles,
		Permissions: actorPermissions,
		SessionID:   newID.String(),
	}, newID, now)
	if err != nil {
		return Refreshed{}, err
	}

	return Refreshed{
		AccessToken:      access,
		TokenType:        jwtutils.BearerScheme,
		AccessExpiresIn:  int32(s.issuer.AccessTokenTTL().Seconds()),
		RefreshExpiresIn: int32(s.issuer.SessionLifetime(ctx, false).Seconds()),
		RefreshToken:     refresh.Plain,
		SessionID:        newID.String(),
		User:             view,
	}, nil
}
