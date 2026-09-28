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

	"github.com/riipandi/tango/internal/datastore"
)

// The oauth2_sessions rows the protocol managers write. One kind per
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
		`INSERT INTO public.oauth2_sessions (kind, key, request_id, client_id, request_data, expires_at)
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
	var data []byte
	err := st.pool.QueryRow(ctx,
		`SELECT request_data FROM public.oauth2_sessions WHERE kind = $1 AND key = $2 AND active`,
		kind, key).Scan(&data)
	if err != nil {
		if errors.Is(err, datastore.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return goidc.ErrNotFound
		}
		return err
	}
	return json.Unmarshal(data, value)
}

// loadPointer reads a pointer row and answers the row it names.
func (st protocolStore) loadPointer(ctx context.Context, kind, hash string) (tokenPointer, error) {
	var pointer tokenPointer
	err := st.load(ctx, kind, hash, &pointer)
	return pointer, err
}

func (st protocolStore) delete(ctx context.Context, kind, key string) error {
	_, err := st.pool.Exec(ctx, `DELETE FROM public.oauth2_sessions WHERE kind = $1 AND key = $2`, kind, key)
	return err
}

// hashToken is the SHA-256 digest a pointer row is keyed by.
func hashToken(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// grantStore is the goidc.GrantManager and the two lookups the code and
// refresh grants resolve by. The grant's own tokens are stripped from the
// stored document — the pointer rows are their only database presence.
type grantStore struct {
	protocolStore
}

func (s grantStore) SaveGrant(ctx context.Context, grant *goidc.Grant) error {
	stored := *grant
	stored.AuthCode = ""
	stored.RefreshToken = ""
	if err := s.save(ctx, sessionKindGrant, grant.ID, grant.ClientID, grant.RefreshTokenExpiresAt, &stored); err != nil {
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
	var grant goidc.Grant
	if err := s.load(ctx, sessionKindGrant, id, &grant); err != nil {
		return nil, err
	}
	return &grant, nil
}

func (s grantStore) grantByPointer(ctx context.Context, kind, presented string) (*goidc.Grant, error) {
	pointer, err := s.loadPointer(ctx, kind, hashToken(presented))
	if err != nil {
		return nil, err
	}
	return s.Grant(ctx, pointer.GrantID)
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
