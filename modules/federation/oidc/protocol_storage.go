package oidc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/luikyv/go-oidc/pkg/goidc"

	"github.com/riipandi/saka/framework/datastore"
)

// The oauth_sessions rows the protocol managers write. One kind per
// object; the pointer kinds resolve a presented token to the row that
// owns it, keyed by the token's SHA-256 so no presented credential sits
// in the database in plain text.
const (
	sessionKindGrant     = "grant"
	sessionKindAuthn     = "authn"
	sessionKindLogout    = "logout"
	sessionKindAuthCode  = "authcode"
	sessionKindRefresh   = "refresh"
	sessionKindPAR       = "par"
	sessionKindDevice    = "device"
	sessionKindUserCode  = "usercode"
	sessionKindDeviceCod = "devicecode"
)

// tokenPointer is the request_data of a pointer row: the row it resolves
// to, plus the client the FK and the check constraint name.
type tokenPointer struct {
	GrantID   string `json:"grant_id"`
	SessionID string `json:"session_id,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
}

// protocolStore is the pgx surface every protocol manager shares. The
// query runs on the pool — protocol state is not part of any business
// transaction the features own.
type protocolStore struct {
	pool *datastore.Postgres
}

// save writes one object row. An existing (kind, key) is a rewrite — the
// library saves the same object as its state moves.
func (st protocolStore) save(ctx context.Context, kind, key, clientID string, expiresAt int, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var expires any
	if expiresAt != 0 {
		expires = time.Unix(int64(expiresAt), 0).UTC()
	}
	var client any
	if clientID != "" {
		client = clientID
	}
	_, err = st.pool.Exec(ctx,
		`INSERT INTO public.oauth_sessions (kind, key, request_id, client_id, request_data, expires_at)
		 VALUES ($1, $2, $1, $3, $4, $5)
		 ON CONFLICT (kind, key) DO UPDATE
		 SET request_data = EXCLUDED.request_data, client_id = EXCLUDED.client_id,
		     expires_at = EXCLUDED.expires_at, active = TRUE`,
		kind, key, client, data, expires)
	return err
}

// savePointer writes one pointer row, the hash the key.
func (st protocolStore) savePointer(ctx context.Context, kind, hash, clientID string, expiresAt int, pointer tokenPointer) error {
	return st.save(ctx, kind, hash, clientID, expiresAt, pointer)
}

// load reads one object row and renders it onto the value. An unknown key
// is goidc.ErrNotFound, the refusal the managers' contract names.
func (st protocolStore) load(ctx context.Context, kind, key string, value any) error {
	data, err := st.loadDocument(ctx, kind, key)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

// protocolSkewAllowanceSeconds is how far past the column's expiry a row
// may still answer a lookup. The timestamps are written from the
// application's clock while the filter compares against the database's, so
// a row a skewed clock still considers live must not be refused outright;
// beyond the allowance the row is dead regardless of what the payload
// carries, and the lookup refuses it even before the sweep reaps it. The
// sweep's grace is wider still, so nothing is deleted while a lookup could
// accept it.
const protocolSkewAllowanceSeconds = 120

// loadDocument reads one object row's raw document. The bytes are what a
// compare-and-swap save expects the row to still hold. An expired row
// answers not-found here, so a timestamp the payload check missed — and a
// row the sweep has not reached yet — fails closed all the same.
func (st protocolStore) loadDocument(ctx context.Context, kind, key string) ([]byte, error) {
	var data []byte
	err := st.pool.QueryRow(ctx,
		`SELECT request_data FROM public.oauth_sessions
		 WHERE kind = $1 AND key = $2 AND active
		   AND (expires_at IS NULL OR expires_at > now() - make_interval(secs => $3))`,
		kind, key, protocolSkewAllowanceSeconds).Scan(&data)
	if err != nil {
		if errors.Is(err, datastore.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return nil, goidc.ErrNotFound
		}
		return nil, err
	}
	return data, nil
}

// saveCAS rewrites one row only while the row still holds the document
// the caller read. Zero rows affected means another request wrote the
// row in between — the caller's state is stale and the save is refused.
func (st protocolStore) saveCAS(ctx context.Context, kind, key, clientID string, expiresAt int, data, expected []byte) (bool, error) {
	var expires any
	if expiresAt != 0 {
		expires = time.Unix(int64(expiresAt), 0).UTC()
	}
	var client any
	if clientID != "" {
		client = clientID
	}
	tag, err := st.pool.Exec(ctx,
		`UPDATE public.oauth_sessions
		 SET request_data = $1, client_id = $2, expires_at = $3
		 WHERE kind = $4 AND key = $5 AND request_data = $6`,
		data, client, expires, kind, key, expected)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// loadPointer reads a pointer row and answers the row it names.
func (st protocolStore) loadPointer(ctx context.Context, kind, hash string) (tokenPointer, error) {
	var pointer tokenPointer
	err := st.load(ctx, kind, hash, &pointer)
	return pointer, err
}

func (st protocolStore) delete(ctx context.Context, kind, key string) error {
	_, err := st.pool.Exec(ctx, `DELETE FROM public.oauth_sessions WHERE kind = $1 AND key = $2`, kind, key)
	return err
}

// hashToken is the SHA-256 digest a pointer row is keyed by.
func hashToken(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// grantStore is the goidc.GrantManager and the two lookups the code and
// refresh grants resolve by. The grant document keeps the live refresh
// token — the token endpoint returns it on the authorization-code
// exchange and every rotation — while the pointer rows carry only its
// hash; the authorization code is stripped, its one-time redemption is
// the pointer row's whole job.
type grantStore struct {
	protocolStore
}

// ErrGrantConcurrentlyModified is a save whose request read a grant
// document that another request has since rewritten. For a one-time
// grant that is the second redemption losing the race — the code is
// spent, no second token may be minted.
var ErrGrantConcurrentlyModified = errors.New("oidc: the grant changed while this request was working on it")

func (s grantStore) SaveGrant(ctx context.Context, grant *goidc.Grant) error {
	stored := *grant
	stored.AuthCode = ""
	data, marshalErr := json.Marshal(&stored)
	if marshalErr != nil {
		return marshalErr
	}

	// A rotation retires the pointer the request loaded: the presented
	// token's row goes away as the replacement lands, so the retired
	// token redeems nothing and a replay finds not-found.
	if rotations := refreshRotationsFrom(ctx); rotations != nil && grant.RefreshToken != "" {
		for _, retired := range rotations.retired() {
			if retired != hashToken(grant.RefreshToken) {
				if err := s.delete(ctx, sessionKindRefresh, retired); err != nil {
					return err
				}
			}
		}
	}

	// A request that armed the compare-and-swap and has already read
	// this grant saves against the document it read: the write lands
	// only while the row still holds it, which is what makes a one-time
	// grant's consumption atomic. A grant the request created itself
	// carries no snapshot — the row is its own, the plain write holds.
	if snapshots := grantSnapshotsFrom(ctx); snapshots != nil {
		expected, known := snapshots.expected(grant.ID)
		if known {
			saved, err := s.saveCAS(ctx, sessionKindGrant, grant.ID, grant.ClientID, grant.RefreshTokenExpiresAt, data, expected)
			if err != nil {
				return err
			}
			if !saved {
				return ErrGrantConcurrentlyModified
			}
			snapshots.remember(grant.ID, data)
		} else if err := s.save(ctx, sessionKindGrant, grant.ID, grant.ClientID, grant.RefreshTokenExpiresAt, &stored); err != nil {
			return err
		}
	} else if err := s.save(ctx, sessionKindGrant, grant.ID, grant.ClientID, grant.RefreshTokenExpiresAt, &stored); err != nil {
		return err
	}
	// The pointers track the tokens as they are issued; a consumed code
	// loses its pointer, so a replay is a not-found rather than a row.
	if grant.AuthCode != "" && grant.AuthCodeConsumedAt == 0 {
		if err := s.savePointer(ctx, sessionKindAuthCode, hashToken(grant.AuthCode), grant.ClientID, grant.AuthCodeExpiresAt,
			tokenPointer{GrantID: grant.ID, ClientID: grant.ClientID}); err != nil {
			return err
		}
	}
	if grant.AuthCodeConsumedAt != 0 {
		if err := s.delete(ctx, sessionKindAuthCode, hashToken(grant.AuthCode)); err != nil {
			return err
		}
	}
	if grant.RefreshToken != "" {
		if err := s.savePointer(ctx, sessionKindRefresh, hashToken(grant.RefreshToken), grant.ClientID, grant.RefreshTokenExpiresAt,
			tokenPointer{GrantID: grant.ID, ClientID: grant.ClientID}); err != nil {
			return err
		}
	}
	// A device grant resolves by its device code the same way a code grant
	// resolves by its authorization code; a consumed device code loses its
	// pointer, so the second redemption is a not-found.
	if grant.DeviceCode != "" && grant.DeviceCodeConsumedAt == 0 {
		if err := s.savePointer(ctx, sessionKindDeviceCod, hashToken(grant.DeviceCode), grant.ClientID, grant.DeviceCodeExpiresAt,
			tokenPointer{GrantID: grant.ID, ClientID: grant.ClientID}); err != nil {
			return err
		}
	}
	if grant.DeviceCodeConsumedAt != 0 {
		if err := s.delete(ctx, sessionKindDeviceCod, hashToken(grant.DeviceCode)); err != nil {
			return err
		}
	}
	return nil
}

func (s grantStore) Grant(ctx context.Context, id string) (*goidc.Grant, error) {
	data, err := s.loadDocument(ctx, sessionKindGrant, id)
	if err != nil {
		return nil, err
	}
	var grant goidc.Grant
	if err := json.Unmarshal(data, &grant); err != nil {
		return nil, err
	}
	// The document as read is the snapshot the request's own save will
	// demand — the read half of the compare-and-swap.
	if snapshots := grantSnapshotsFrom(ctx); snapshots != nil {
		snapshots.remember(id, data)
	}
	return &grant, nil
}

func (s grantStore) grantByPointer(ctx context.Context, kind, presented string) (*goidc.Grant, error) {
	pointer, err := s.loadPointer(ctx, kind, hashToken(presented))
	if err != nil {
		return nil, err
	}
	grant, err := s.Grant(ctx, pointer.GrantID)
	if err != nil {
		return nil, err
	}
	// A refresh-token lookup names the pointer its rotation retires: the
	// save that swaps the token in deletes this one, so the retired
	// token answers not-found and a replayed one finds no row.
	if kind == sessionKindRefresh {
		if rotations := refreshRotationsFrom(ctx); rotations != nil {
			rotations.remember(hashToken(presented))
		}
	}
	return grant, nil
}

func (s grantStore) GrantByAuthCode(ctx context.Context, code string) (*goidc.Grant, error) {
	return s.grantByPointer(ctx, sessionKindAuthCode, code)
}

func (s grantStore) GrantByRefreshToken(ctx context.Context, token string) (*goidc.Grant, error) {
	return s.grantByPointer(ctx, sessionKindRefresh, token)
}

func (s grantStore) GrantByDeviceCode(ctx context.Context, deviceCode string) (*goidc.Grant, error) {
	return s.grantByPointer(ctx, sessionKindDeviceCod, deviceCode)
}

// authnStore is the goidc.AuthManager and the PAR lookup. The PAR id has
// its own pointer, written beside the session.
type authnStore struct {
	protocolStore
}

func (s authnStore) SaveSession(ctx context.Context, session *goidc.AuthnSession) error {
	if err := s.save(ctx, sessionKindAuthn, session.ID, session.ClientID, session.ExpiresAt, session); err != nil {
		return err
	}
	if session.PushedAuthReqID != "" {
		return s.savePointer(ctx, sessionKindPAR, session.PushedAuthReqID, session.ClientID, session.ExpiresAt,
			tokenPointer{SessionID: session.ID, ClientID: session.ClientID})
	}
	return nil
}

func (s authnStore) Session(ctx context.Context, id string) (*goidc.AuthnSession, error) {
	var session goidc.AuthnSession
	if err := s.load(ctx, sessionKindAuthn, id, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s authnStore) SessionByPushedAuthReqID(ctx context.Context, pushedAuthReqID string) (*goidc.AuthnSession, error) {
	pointer, err := s.loadPointer(ctx, sessionKindPAR, pushedAuthReqID)
	if err != nil {
		return nil, err
	}
	return s.Session(ctx, pointer.SessionID)
}

// GrantByAuthCode completes the AuthManager contract: the authorization
// code's grant is the grant store's pointer row.
func (s authnStore) GrantByAuthCode(ctx context.Context, code string) (*goidc.Grant, error) {
	return grantStore(s).GrantByAuthCode(ctx, code)
}

// deviceStore is the goidc.DeviceAuthManager. The device and user codes
// ride the same pointer pattern as the authorization code: the session
// row keys by its own id, the codes resolve through hashed pointer rows,
// so no presented code sits in the database in plain text.
type deviceStore struct {
	protocolStore
}

func (s deviceStore) SaveSession(ctx context.Context, session *goidc.AuthnSession) error {
	if err := s.save(ctx, sessionKindDevice, session.ID, session.ClientID, session.ExpiresAt, session); err != nil {
		return err
	}
	if session.DeviceCode != "" {
		if err := s.savePointer(ctx, sessionKindDeviceCod, hashToken(session.DeviceCode), session.ClientID, session.ExpiresAt,
			tokenPointer{SessionID: session.ID, ClientID: session.ClientID}); err != nil {
			return err
		}
	}
	if session.UserCode == "" {
		return nil
	}
	// The user code is the credential a human carries to the browser, so
	// a re-entered code re-points at the newest session holding it and the
	// superseded pointer dies — two live sessions never share one code.
	if err := s.delete(ctx, sessionKindUserCode, hashToken(session.UserCode)); err != nil {
		return err
	}
	return s.savePointer(ctx, sessionKindUserCode, hashToken(session.UserCode), session.ClientID, session.ExpiresAt,
		tokenPointer{SessionID: session.ID, ClientID: session.ClientID})
}

func (s deviceStore) Session(ctx context.Context, id string) (*goidc.AuthnSession, error) {
	var session goidc.AuthnSession
	if err := s.load(ctx, sessionKindDevice, id, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s deviceStore) sessionByPointer(ctx context.Context, kind, code string) (*goidc.AuthnSession, error) {
	pointer, err := s.loadPointer(ctx, kind, hashToken(code))
	if err != nil {
		return nil, err
	}
	return s.Session(ctx, pointer.SessionID)
}

func (s deviceStore) SessionByUserCode(ctx context.Context, userCode string) (*goidc.AuthnSession, error) {
	return s.sessionByPointer(ctx, sessionKindUserCode, userCode)
}

func (s deviceStore) SessionByDeviceCode(ctx context.Context, deviceCode string) (*goidc.AuthnSession, error) {
	return s.sessionByPointer(ctx, sessionKindDeviceCod, deviceCode)
}

func (s deviceStore) GrantByDeviceCode(ctx context.Context, deviceCode string) (*goidc.Grant, error) {
	return grantStore(s).GrantByDeviceCode(ctx, deviceCode)
}

// logoutStore is the goidc.LogoutManager.
type logoutStore struct {
	protocolStore
}

func (s logoutStore) SaveLogoutSession(ctx context.Context, session *goidc.LogoutSession) error {
	return s.save(ctx, sessionKindLogout, session.ID, session.ClientID, session.ExpiresAt, session)
}

func (s logoutStore) LogoutSession(ctx context.Context, id string) (*goidc.LogoutSession, error) {
	var session goidc.LogoutSession
	if err := s.load(ctx, sessionKindLogout, id, &session); err != nil {
		return nil, err
	}
	return &session, nil
}
