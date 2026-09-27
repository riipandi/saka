package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/riipandi/tango/pkg/responder"

	"go.jetify.com/typeid"
)

// The failures the service defines. The handler maps them onto the codes the
// Connect protocol carries; the service defines what happened, not how it is
// answered.
var (
	// ErrSessionEnded is a session that no longer authenticates: it was
	// revoked, or its window closed. A refresh token that names one answers
	// the same failure an unknown token does.
	ErrSessionEnded = errors.New("session: session has ended")

	// ErrSessionNotFound is an identifier that names no session, or one
	// another account holds — the two are the same answer, because the
	// owner's list is the only way to learn which sessions exist. It is a
	// different failure from ErrSessionEnded on purpose: an ended session of
	// your own is an authentication state, a session you cannot name is a
	// target that does not exist, and the two wire codes must not share a
	// shape.
	ErrSessionNotFound = errors.New("session: session not found")
)

// Issuer is the signing half of the authwall the session lifecycle calls: the
// access token a renewal answers with, and the two lifetimes the session
// window is written from. It is an interface this package defines rather than
// a package it imports — the opening and the renewal must not drift apart,
// and a seam is what keeps them one vocabulary without an import cycle, since
// the sign-in issuer itself reads this package's schema.
type Issuer interface {
	// SignSessionToken mints the access token from the claims the caller
	// assembled, for the session identifier the token carries in `sid`.
	SignSessionToken(ctx context.Context, subject string, claims jwtutils.AccessClaims, sessionID SessionID, at time.Time) (string, error)
	// SessionLifetime is the window a session's renewal writes, by the
	// remember flag the opening recorded.
	SessionLifetime(remember bool) time.Duration
	// AccessTokenTTL is the lifetime the access token is signed with, so a
	// renewal answers the same expires_in the sign-in does.
	AccessTokenTTL() time.Duration
}

// Service carries the rules of the session lifecycle: what ends a session,
// what a renewal costs, and what a change records. The repository carries the
// SQL; the issuer carries the signing and the lifetimes.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// issuer mints the access token a renewal answers with and owns the two
	// lifetimes the session window is written from — the opening and the
	// renewal must not drift apart, which is why they are one vocabulary.
	issuer Issuer
	users  *user.Service
	audit  *audit.Recorder
	log    *slog.Logger

	// now is the instant the service's decisions read. It is a field so a
	// test can hold the clock still.
	now func() time.Time
}

// NewService builds the service over the shared pool.
func NewService(pool *datastore.Postgres, issuer Issuer, users *user.Service, recorder *audit.Recorder, log *slog.Logger) *Service {
	return &Service{
		pool:   pool,
		repo:   NewRepository(),
		issuer: issuer,
		users:  users,
		audit:  recorder,
		log:    log,
		now:    time.Now,
	}
}

// SignedOut is what a sign-out answers: the state the session was in, so the
// response can say something truer than "it worked".
type SignedOut struct {
	// Already says the row was stamped before this call: the caller's intent
	// is the state the session is already in, and nothing was written.
	Already bool

	// Expired says the window had closed before this call. The stamp is
	// still written — the sign-out closes the book an expiry left open.
	Expired bool
}

// SignOut ends the session the access token names. The stamp is the write,
// the refresh token dies with it, and the access token keeps working until
// its own expiry — the statelessness the protocol settles. A session that is
// already ended is the same success: the caller's intent is the state the
// session is in, nothing is written, and the answer says so.
func (s *Service) SignOut(ctx context.Context, callerSession string, callerID string) (SignedOut, error) {
	sid, err := parseSessionID(callerSession)
	if err != nil {
		return SignedOut{}, ErrSessionEnded
	}
	userID, callerErr := callerUUID(callerID)
	if callerErr != nil {
		return SignedOut{}, ErrSessionEnded
	}

	var outcome SignedOut
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, getErr := s.repo.GetSession(ctx, tx, sid)
		if errors.Is(getErr, datastore.ErrNoRows) {
			return ErrSessionEnded
		}
		if getErr != nil {
			return getErr
		}

		// A row that was stamped before this call is the caller's intent
		// already satisfied: the answer reports it and writes nothing.
		if row.RevokedAt != nil {
			outcome = SignedOut{Already: true}
			return nil
		}

		now := s.now()
		ended, revokeErr := s.repo.Revoke(ctx, tx, sid, userID, now)
		if revokeErr != nil {
			return revokeErr
		}
		if !ended {
			outcome = SignedOut{Already: true}
			return nil
		}

		outcome = SignedOut{Expired: !row.ExpiresAt.After(now)}

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventSignOut,
			Status:       audit.StatusSuccess,
			UserID:       row.UserID.String(),
			ResourceType: ResourceSession,
			ResourceID:   sid.UUID(),
			Payload: map[string]string{
				"provider": row.Provider,
			},
		})
		return nil
	})
	if err != nil {
		return SignedOut{}, err
	}
	return outcome, nil
}

// GetSession answers the session the access token names and the account it
// belongs to — the "who am I" a client asks when it resumes. A session that
// has ended answers the ended failure, which is the signal a resuming client
// needs: the token verified, the session behind it did not survive.
func (s *Service) GetSession(ctx context.Context, callerSession string) (SessionSchema, user.UserView, error) {
	sid, err := parseSessionID(callerSession)
	if err != nil {
		return SessionSchema{}, user.UserView{}, ErrSessionEnded
	}

	row, err := s.repo.GetSession(ctx, s.pool, sid)
	if errors.Is(err, datastore.ErrNoRows) {
		return SessionSchema{}, user.UserView{}, ErrSessionEnded
	}
	if err != nil {
		return SessionSchema{}, user.UserView{}, err
	}
	if row.RevokedAt != nil || !row.ExpiresAt.After(s.now()) {
		return SessionSchema{}, user.UserView{}, ErrSessionEnded
	}

	view, err := user.ReadAccount(ctx, s.pool, row.UserID)
	if err != nil {
		return SessionSchema{}, user.UserView{}, err
	}
	return row, view, nil
}

// liveOwnSession is the gate the account-scoped procedures read through: a
// caller whose own session row is no longer live — stamped, or past its
// window — holds a credential the protocol still verifies but the session
// surface no longer honours. The gate is the session surface's alone: the
// rest of the API stays stateless, the trade the protocol settled.
func (s *Service) liveOwnSession(ctx context.Context, db datastore.Querier, sid SessionID) error {
	row, err := s.repo.GetSession(ctx, db, sid)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrSessionEnded
	}
	if err != nil {
		return err
	}
	if row.RevokedAt != nil || !row.ExpiresAt.After(s.now()) {
		return ErrSessionEnded
	}
	return nil
}

// ListSessions answers one page of the account's sessions, newest first,
// ended ones included. The gate is the caller's own session: a sign-out ends
// the holder's view of the list along with everything else the session
// surface serves.
func (s *Service) ListSessions(ctx context.Context, callerSession, callerID string, page, limit int) ([]SessionSchema, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)

	sid, err := parseSessionID(callerSession)
	if err != nil {
		return nil, responder.Pagination{}, ErrSessionEnded
	}
	if liveErr := s.liveOwnSession(ctx, s.pool, sid); liveErr != nil {
		return nil, responder.Pagination{}, liveErr
	}

	userID, callerErr := callerUUID(callerID)
	if callerErr != nil {
		return nil, responder.Pagination{}, ErrSessionEnded
	}

	rows, total, err := s.repo.ListOwn(ctx, s.pool, userID, responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	return rows, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
}

// RevokeSession ends one of the account's sessions. Ending the current one is
// what SignOut does; ending another is how a holder closes a device they no
// longer hold. A session that is not the account's answers the ended failure
// — the not-found shape the handler maps, because the caller learns nothing
// about whether the session exists — and an already-ended one is the same
// success that records nothing.
func (s *Service) RevokeSession(ctx context.Context, callerSession, callerID, target string) error {
	sid, err := parseSessionID(callerSession)
	if err != nil {
		return ErrSessionEnded
	}
	userID, callerErr := callerUUID(callerID)
	if callerErr != nil {
		return ErrSessionEnded
	}
	targetID, err := parseSessionID(target)
	if err != nil {
		return ErrSessionNotFound
	}

	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if liveErr := s.liveOwnSession(ctx, tx, sid); liveErr != nil {
			return liveErr
		}
		row, getErr := s.repo.GetSession(ctx, tx, targetID)
		if errors.Is(getErr, datastore.ErrNoRows) {
			return ErrSessionNotFound
		}
		if getErr != nil {
			return getErr
		}
		if row.UserID != userID {
			return ErrSessionNotFound
		}

		ended, revokeErr := s.repo.Revoke(ctx, tx, targetID, userID, s.now())
		if revokeErr != nil {
			return revokeErr
		}
		if !ended {
			return nil
		}

		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventSessionRevoked,
			Status:       audit.StatusSuccess,
			UserID:       userID.String(),
			ResourceType: ResourceSession,
			ResourceID:   targetID.UUID(),
			Payload: map[string]string{
				"provider":    row.Provider,
				"was_current": fmt.Sprint(targetID == sid),
			},
		})
		return nil
	})
}

// Refreshed is what a renewal answers: the fresh pair and the session it
// kept.
type Refreshed struct {
	AccessToken      string
	TokenType        string
	AccessExpiresIn  int32
	RefreshExpiresIn int32
	RefreshToken     string
	SessionID        string
	User             user.UserView
}

// SignOutOtherSessions ends every live session of the account except the one
// the access token names. Each ended row is stamped and recorded; the count
// is what the call actually ended, not what it looked at.
func (s *Service) SignOutOtherSessions(ctx context.Context, callerSession, callerID string) (int, error) {
	return s.revokeBulk(ctx, callerSession, callerID, "sign_out_others", true)
}

// SignOutAllSessions ends every live session of the account, the one the
// access token names included. The access token itself keeps working until
// its own expiry — the statelessness the protocol settles — so the caller
// that means to discard its credential drops the token pair too.
func (s *Service) SignOutAllSessions(ctx context.Context, callerSession, callerID string) (int, error) {
	return s.revokeBulk(ctx, callerSession, callerID, "sign_out_all", false)
}

// revokeBulk is the write the two bulk sign-outs share: the reason names the
// scope in the audit payload, and `keepCurrent` decides whether the session
// the caller is holding survives the sweep.
func (s *Service) revokeBulk(ctx context.Context, callerSession, callerID, reason string, keepCurrent bool) (int, error) {
	sid, err := parseSessionID(callerSession)
	if err != nil {
		return 0, ErrSessionEnded
	}
	userID, callerErr := callerUUID(callerID)
	if callerErr != nil {
		return 0, ErrSessionEnded
	}

	var keep *SessionID
	if keepCurrent {
		keep = &sid
	}

	revoked := 0
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if liveErr := s.liveOwnSession(ctx, tx, sid); liveErr != nil {
			return liveErr
		}
		rows, bulkErr := s.repo.RevokeLiveForUser(ctx, tx, userID, keep, userID, s.now())
		if bulkErr != nil {
			return bulkErr
		}
		for _, row := range rows {
			s.audit.Record(ctx, tx, audit.Entry{
				Event:        audit.EventSessionRevoked,
				Status:       audit.StatusSuccess,
				UserID:       userID.String(),
				ResourceType: ResourceSession,
				ResourceID:   row.ID.UUID(),
				Payload: map[string]string{
					"provider": row.Provider,
					"reason":   reason,
				},
			})
		}
		revoked = len(rows)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return revoked, nil
}

// Refresh exchanges a refresh token for a fresh pair. The token is rotated in
// place — the row keeps its identifier, the secret and the window are
// replaced — so the session a client opened keeps its identity across
// renewals, and the old access token's claims name a session that still
// exists.
//
// The rotation's write carries the gate in its WHERE clause, so a session
// that was revoked between the read and the write costs the new secret and
// nothing else: the renewal dies before it spent anything, and the caller
// signs in again.
//
// No audit record is written: a renewal is the session continuing, not a
// happening an operator audits for, and one line per heartbeat would drown
// the log in the very renewals it exists to see past.
func (s *Service) Refresh(ctx context.Context, presented string) (Refreshed, error) {
	if presented == "" {
		return Refreshed{}, ErrSessionEnded
	}

	now := s.now()
	row, err := s.repo.FindActiveByTokenHash(ctx, s.pool, crypto.HashRefreshToken(presented), now)
	if errors.Is(err, datastore.ErrNoRows) {
		return Refreshed{}, ErrSessionEnded
	}
	if err != nil {
		return Refreshed{}, err
	}

	// The account's state is the issuer's check, the way every way of
	// continuing a session refuses the same: a disabled or banned account's
	// renewal ends here, not at the next request.
	view, err := user.ReadAccount(ctx, s.pool, row.UserID)
	if err != nil {
		return Refreshed{}, err
	}
	if view.Disabled || bannedAt(view, now) {
		return Refreshed{}, ErrSessionEnded
	}

	replacement, err := crypto.NewRefreshTokenPair()
	if err != nil {
		return Refreshed{}, fmt.Errorf("session: refresh token: %w", err)
	}

	var rotated bool
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		var rotateErr error
		rotated, rotateErr = s.repo.Rotate(ctx, tx, row.ID, replacement.Hash, now,
			now.Add(s.issuer.SessionLifetime(row.Remember)))
		return rotateErr
	})
	if err != nil {
		return Refreshed{}, err
	}
	if !rotated {
		// The session ended between the read and the write: the new secret
		// is thrown away and the caller signs in again.
		return Refreshed{}, ErrSessionEnded
	}

	roles, permissions, grantsErr := user.LoadGrants(ctx, s.pool, row.UserID)
	if grantsErr != nil {
		return Refreshed{}, grantsErr
	}

	access, err := s.issuer.SignSessionToken(ctx, view.ID, jwtutils.AccessClaims{
		Email:       view.Email,
		Username:    view.Username,
		DisplayName: view.DisplayName,
		Roles:       roles,
		Permissions: permissions,
		SessionID:   row.ID.String(),
		// The delegation survives its renewal: the row's impersonated_by is
		// the durable fact the actor pair is re-signed from, so the rotated
		// token keeps naming the administrator behind it.
		ActorID: actorIDString(row.ImpersonatedBy),
	}, row.ID, now)
	if err != nil {
		return Refreshed{}, err
	}

	return Refreshed{
		AccessToken:      access,
		TokenType:        jwtutils.BearerScheme,
		AccessExpiresIn:  int32(s.issuer.AccessTokenTTL().Seconds()),
		RefreshExpiresIn: int32(s.issuer.SessionLifetime(row.Remember).Seconds()),
		RefreshToken:     replacement.Plain,
		SessionID:        row.ID.String(),
		User:             view,
	}, nil
}

// RevokeAllForUser stamps the end of every live session the account holds —
// the write a ban makes in its own transaction. The ender is recorded as the
// account itself: the sessions did nothing wrong, the account's standing
// changed. The count answers the ban's audit record.
func (s *Service) RevokeAllForUser(ctx context.Context, tx datastore.Querier, userID uuid.UUID) (int, error) {
	rows, err := s.repo.RevokeLiveForUser(ctx, tx, userID, nil, userID, s.now())
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

// bannedAt reports whether the account sits inside its ban window. A ban
// without an expiry never lifts by itself — the same rule the sign-in issuer
// applies.
func bannedAt(view user.UserView, at time.Time) bool {
	return view.BannedAt != nil && (view.BanExpires == nil || view.BanExpires.After(at))
}

// addrPtr converts the caller address to the INET column's form, nil when the
// transport did not supply one. The same boundary the sign-in issuer keeps —
// every session row is written through one of the two.
func addrPtr(raw string) *netip.Addr {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	return &addr
}

// actorIDString is the delegated row's actor as the claims carry it — the
// wire TypeID form, and empty on a session that is nobody's delegation, so
// the omitted claim stays omitted on every token that is not one.
func actorIDString(by *uuid.UUID) string {
	if by == nil {
		return ""
	}
	return user.FormatID(*by)
}

// callerUUID turns the claims' subject into the key the rows carry. The
// subject travels in the wire form — the TypeID the token carries — and the
// rows keep their UUID, so the boundary is this one function.
func callerUUID(wire string) (uuid.UUID, error) {
	id, err := user.UUIDFromWire(wire)
	if err != nil {
		return uuid.Nil(), ErrSessionEnded
	}
	return id, nil
}

func parseSessionID(raw string) (SessionID, error) {
	parsed, err := typeid.Parse[SessionID](raw)
	if err != nil {
		return SessionID{}, err
	}
	return parsed, nil
}
