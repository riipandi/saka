package oidc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"uuid"

	"github.com/luikyv/go-oidc/pkg/goidc"

	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// The two authentication policies the authorization endpoint walks: the
// sign-in policy answers the browser's first visit, the interaction policy
// answers the SPA's callback with the credential and the consent decision.
const (
	policySignInID       = "saka-sign-in"
	policyInteractionID  = "saka-interaction"
	policyDeviceID       = "saka-device"
	consentBodyByteLimit = 8192
)

// consentDecision is the JSON body the SPA posts to the callback: the
// scopes the account agreed to hand the client, when it agreed at all.
type consentDecision struct {
	Scopes []string `json:"scopes,omitempty"`
}

// oidcSessionCookie is the OP's browser-session marker. The provider holds
// no interactive session the SPA relies on — the sign-in credential rides
// the request — but the `prompt=none` semantics OIDC Core §3.1.2.1 asks
// for need the one answer only a browser-side state can give: whether this
// user agent already authenticated here. A completed authorization leaves
// the marker; a prompt=none request reads it or refuses with
// login_required.
const oidcSessionCookie = "saka_oidc_session"

// oidcSessionCookieLifetimeSecs is the marker's window — the same order the
// engine's authentication sessions live for, long enough that a relying
// party's silent renewals across a workday meet an existing session.
const oidcSessionCookieLifetimeSecs = 28800

// authTimeStoreKey is the key the completed authentication stamps its
// instant under — the value the ID token's auth_time claim carries, the
// claim `max_age` is judged against.
const authTimeStoreKey = "auth_time"

// signInPolicy runs while the browser stands on the authorization
// endpoint itself. Without a credential it redirects to the SPA's
// interaction page — the provider keeps the session pending and writes
// nothing, so the redirect here is the whole response. With one, the
// decision proceeds on the spot.
func signInPolicy(service *Service) goidc.AuthnPolicy {
	return goidc.NewPolicy(policySignInID,
		func(r *http.Request, _ *goidc.AuthnSession, _ *goidc.Client) bool {
			return r.URL.Path == protocolPrefix+protocolAuthorizeEndpoint
		},
		func(w http.ResponseWriter, r *http.Request, session *goidc.AuthnSession, client *goidc.Client) (goidc.Status, error) {
			// A prompt=none request may never interact: the answer is the
			// session the browser already holds, or the login_required
			// error the specification names — never the interaction page.
			if session.Prompt == goidc.PromptTypeNone {
				return promptNoneAuthentication(service, w, r, session, client)
			}
			// An existing browser session answers without a fresh
			// authentication while the request's max_age still covers the
			// prior authentication instant — the auth_time the tokens
			// carry must not move, and prompt=login asks for exactly the
			// opposite.
			if session.Prompt != goidc.PromptTypeLogin {
				if resumeBrowserSession(service, w, r, session) {
					return goidc.StatusSuccess, nil
				}
			}
			caller, ok := jwtutils.CallerFrom(r.Context())
			if !ok {
				interactionRedirect(w, r, protocolPrefix+protocolAuthorizeEndpoint, session.ID)
				return goidc.StatusPending, nil
			}
			return completeAuthentication(r.Context(), service, w, r, session, client, caller)
		})
}

// resumeBrowserSession answers from the browser-session marker when one
// exists and the request's max_age still covers the authentication instant
// it recorded: the subject carries over, the scopes ride as requested —
// prompt=none semantics without the prompt — and the original auth_time
// stays the one the tokens carry. It answers false when the marker is
// absent, stale, or the request demands a fresh authentication.
func resumeBrowserSession(service *Service, w http.ResponseWriter, r *http.Request, session *goidc.AuthnSession) bool {
	cookie, err := r.Cookie(oidcSessionCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	prior, err := authnStore{protocolStore: protocolStore{pool: service.pool}}.Session(r.Context(), cookie.Value)
	if err != nil || prior.Subject == "" {
		return false
	}
	raw, ok := prior.Store[authTimeStoreKey]
	if !ok {
		return false
	}
	authTime := 0
	switch at := raw.(type) {
	case int:
		authTime = at
	case float64:
		authTime = int(at)
	}
	if authTime <= 0 {
		return false
	}
	if session.MaxAuthnAgeSecs != nil && int(time.Now().Unix()) > authTime+*session.MaxAuthnAgeSecs {
		return false
	}

	carryOverSession(w, session, prior)
	return true
}

// promptNoneAuthentication answers a silent authorization request from the
// browser session alone: the cookie names the authentication session a
// previous completion left behind, its subject and its authentication
// instant carry over — the id_token's auth_time must stay the original
// login's, which is the very claim the request's max_age checks — and the
// request's scopes ride as granted. Anything less is the login_required
// refusal, and the bearer credential deliberately plays no part here:
// prompt=none asks about the browser's state, not the API client's.
func promptNoneAuthentication(service *Service, w http.ResponseWriter, r *http.Request,
	session *goidc.AuthnSession, _ *goidc.Client) (goidc.Status, error) {

	cookie, err := r.Cookie(oidcSessionCookie)
	if err != nil || cookie.Value == "" {
		return goidc.StatusFailure, goidc.NewError(goidc.ErrorCodeLoginRequired,
			"no provider session: the browser has not authenticated here")
	}

	prior, err := authnStore{protocolStore: protocolStore{pool: service.pool}}.Session(r.Context(), cookie.Value)
	if err != nil || prior.Subject == "" {
		return goidc.StatusFailure, goidc.NewError(goidc.ErrorCodeLoginRequired,
			"the provider session the browser named is gone")
	}

	carryOverSession(w, session, prior)
	return goidc.StatusSuccess, nil
}

// carryOverSession finishes the request's session off the prior one: the
// subject and the authentication instant move over untouched — the
// id_token's auth_time must stay the original login's — the scopes ride as
// requested, and the browser-session marker re-points at the session this
// answer completes, so the next silent request names a live row.
func carryOverSession(w http.ResponseWriter, session *goidc.AuthnSession, prior *goidc.AuthnSession) {
	session.Subject = prior.Subject
	session.Username = prior.Username
	session.GrantedScopes = strings.Join(requestedScopes(session), " ")
	if session.Store == nil {
		session.Store = map[string]any{}
	}
	var authTime int
	switch at := prior.Store[authTimeStoreKey].(type) {
	case int:
		authTime = at
	case float64:
		authTime = int(at)
	}
	session.Store[authTimeStoreKey] = authTime
	// The completed session outlives the pending one's window: the
	// browser-session marker names it, so the row must live as long as
	// the cookie does or every later silent request reads a dead row.
	session.ExpiresAt = int(time.Now().Unix()) + oidcSessionCookieLifetimeSecs
	http.SetCookie(w, &http.Cookie{
		Name:     oidcSessionCookie,
		Value:    session.ID,
		Path:     "/",
		MaxAge:   oidcSessionCookieLifetimeSecs,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// stampAuthentication marks the completion the success answers with: the
// authentication instant the ID token's auth_time carries — the value
// `max_age` is judged against — and the browser-session marker a later
// prompt=none request reads.
func stampAuthentication(w http.ResponseWriter, session *goidc.AuthnSession) {
	if session.Store == nil {
		session.Store = map[string]any{}
	}
	now := int(time.Now().Unix())
	session.Store[authTimeStoreKey] = now
	// The completed session outlives the pending one's window: the
	// browser-session marker names it, so the row must live as long as
	// the cookie does or every later silent request reads a dead row.
	session.ExpiresAt = now + oidcSessionCookieLifetimeSecs
	http.SetCookie(w, &http.Cookie{
		Name:     oidcSessionCookie,
		Value:    session.ID,
		Path:     "/",
		MaxAge:   oidcSessionCookieLifetimeSecs,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// interactionPolicy runs on the callback path the SPA posts to.
func interactionPolicy(service *Service) goidc.AuthnPolicy {
	return goidc.NewPolicy(policyInteractionID,
		func(r *http.Request, _ *goidc.AuthnSession, _ *goidc.Client) bool {
			return strings.HasPrefix(r.URL.Path, protocolPrefix+protocolAuthorizeEndpoint+"/")
		},
		func(w http.ResponseWriter, r *http.Request, session *goidc.AuthnSession, client *goidc.Client) (goidc.Status, error) {
			caller, ok := jwtutils.CallerFrom(r.Context())
			if !ok {
				interactionRedirect(w, r, protocolPrefix+protocolAuthorizeEndpoint, session.ID)
				return goidc.StatusPending, nil
			}
			return completeAuthentication(r.Context(), service, w, r, session, client, caller)
		})
}

// devicePolicy runs on the device verification path. The flow enters two
// ways: the browser's first visit — with the user code in the query or
// the callback the prompt page followed — where the account signs in at
// the SPA; and the callback's continuation, where the SPA posts the
// consent decision. The callback is the session's id under the
// verification endpoint, the shape the provider's routes carry.
func devicePolicy(service *Service) goidc.AuthnPolicy {
	return goidc.NewPolicy(policyDeviceID,
		func(r *http.Request, _ *goidc.AuthnSession, _ *goidc.Client) bool {
			return strings.HasPrefix(r.URL.Path, protocolPrefix+protocolDeviceVerificationEndpoint)
		},
		func(w http.ResponseWriter, r *http.Request, session *goidc.AuthnSession, client *goidc.Client) (goidc.Status, error) {
			caller, ok := jwtutils.CallerFrom(r.Context())
			if !ok {
				interactionRedirect(w, r, protocolPrefix+protocolDeviceVerificationEndpoint, session.ID)
				return goidc.StatusPending, nil
			}
			return completeAuthentication(r.Context(), service, w, r, session, client, caller)
		})
}

// completeAuthentication resolves the consent question and finishes the
// session. The scopes the decision grants are the requested ones the
// account agreed to — nothing is granted that was not asked for.
func completeAuthentication(ctx context.Context, service *Service, w http.ResponseWriter, r *http.Request,
	session *goidc.AuthnSession, client *goidc.Client, caller *jwtutils.Caller) (goidc.Status, error) {

	view, viewErr := service.Get(ctx, client.ID)
	if viewErr != nil {
		return goidc.StatusFailure, goidc.WrapError(goidc.ErrorCodeUnauthorizedClient, "unauthorized client",
			errors.New("the client is not known"))
	}
	account := subjectFor(caller)
	// Group restriction gate: a client counts as restricted when its flag is
	// set or its allowed-groups roll carries rows. Restricted clients admit
	// only accounts in an allowed group — flag set with an empty roll admits
	// nobody. The check runs before any grant, consent write, or device
	// approval, so a direct protocol call cannot bypass the catalogue rule.
	restricted := view.IsGroupRestricted || len(view.AllowedGroups) > 0
	admitted, admitErr := service.accountAdmitted(ctx, account.subject, client.ID, restricted)
	if admitErr != nil {
		return goidc.StatusFailure, admitErr
	}
	if !admitted {
		return goidc.StatusFailure, goidc.WrapError(goidc.ErrorCodeAccessDenied, "access denied",
			errors.New("the account is not allowed to authorize this client"))
	}
	isDevice := strings.HasPrefix(r.URL.Path, protocolPrefix+protocolDeviceVerificationEndpoint)
	if view.SkipConsent {
		session.GrantedScopes = strings.Join(requestedScopes(session), " ")
		session.Subject = account.subject
		session.Username = account.username
		if isDevice {
			service.recordDeviceAuthorization(ctx, account.subject, client.ID)
		}
		stampAuthentication(w, session)
		return goidc.StatusSuccess, nil
	}

	if approved, decided := decisionScopes(r, requestedScopes(session)); decided {
		session.GrantedScopes = strings.Join(approved, " ")
		session.Subject = account.subject
		session.Username = account.username
		if err := service.recordAuthorization(ctx, account.subject, client.ID, approved); err != nil {
			return goidc.StatusFailure, err
		}
		if isDevice {
			service.recordDeviceAuthorization(ctx, account.subject, client.ID)
		}
		stampAuthentication(w, session)
		return goidc.StatusSuccess, nil
	}

	// No decision in the body: a consent the account already gave that
	// covers the request ends the question; otherwise the SPA answers
	// with the interaction document and the session stays pending.
	if known, err := service.authorizedScopes(ctx, account.subject, client.ID); err == nil && coversScopes(known, requestedScopes(session)) {
		session.GrantedScopes = strings.Join(requestedScopes(session), " ")
		session.Subject = account.subject
		session.Username = account.username
		if isDevice {
			service.recordDeviceAuthorization(ctx, account.subject, client.ID)
		}
		stampAuthentication(w, session)
		return goidc.StatusSuccess, nil
	}
	writeInteraction(w, http.StatusOK, map[string]any{
		"interaction_id":   session.ID,
		"client_id":        client.ID,
		"client_name":      client.Name,
		"requested_scopes": requestedScopes(session),
		"consent_required": true,
		// Third-party initiated login: the hint the relying party sent
		// — a username or email — prefills or highlights the account
		// the flow is for. The SPA treats it as a display hint only:
		// the signed-in credential decides the subject.
		"login_hint": session.LoginHint,
	})
	return goidc.StatusPending, nil
}

// subject is the account a session is granted to: the UUID the claims
// resolve by, and the username the grant and introspection name.
type subject struct {
	subject  string
	username string
}

func subjectFor(caller *jwtutils.Caller) subject {
	id, err := user.UUIDFromWire(caller.UserID)
	if err != nil {
		return subject{subject: caller.UserID, username: caller.ActorUsername}
	}
	return subject{subject: id.String(), username: caller.ActorUsername}
}

// requestedScopes are the scope words the authorization request asked
// for, minus openid — the protocol grants that one by itself.
func requestedScopes(session *goidc.AuthnSession) []string {
	scopes := strings.Fields(session.Scopes)
	if !slices.Contains(scopes, "openid") {
		scopes = append(scopes, "openid")
	}
	return scopes
}

// decisionScopes reads the body the callback posted. No body is no
// decision — the consent question still stands.
func decisionScopes(r *http.Request, requested []string) ([]string, bool) {
	if r.Body == nil || r.ContentLength == 0 {
		return nil, false
	}
	limited := io.LimitReader(r.Body, consentBodyByteLimit)
	var decision consentDecision
	if err := json.UnmarshalRead(limited, &decision); err != nil {
		return nil, false
	}
	approved := []string{}
	for _, scope := range decision.Scopes {
		if slices.Contains(requested, scope) && !slices.Contains(approved, scope) {
			approved = append(approved, scope)
		}
	}
	if !slices.Contains(approved, "openid") {
		approved = append(approved, "openid")
	}
	return approved, true
}

// coversScopes answers whether the scopes an earlier consent stored
// already cover the request — the state that skips the question.
func coversScopes(known, requested []string) bool {
	for _, scope := range requested {
		if !slices.Contains(known, scope) {
			return false
		}
	}
	return len(known) > 0
}

// interactionRedirect sends the browser to the SPA's interaction page
// with the callback the flow resumes at.
func interactionRedirect(w http.ResponseWriter, r *http.Request, endpoint, sessionID string) {
	callback := endpoint + "/" + sessionID
	http.Redirect(w, r, interactionPath+"?callback="+url.QueryEscape(callback), http.StatusFound)
}

// writeInteraction answers the SPA in its own dialect.
func writeInteraction(w http.ResponseWriter, status int, body map[string]any) {
	data, err := json.Marshal(body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// clientSecretVerifier matches a presented secret against every live
// hash the client's credentials document carries. Each hash names its
// algorithm: a `sha256` entry is the plain hex digest the management
// surface stores, anything else a PHC string the password hasher reads —
// the shape a carried-over credential may hold.
func clientSecretVerifier(ctx context.Context, stored, presented string) error {
	for _, hash := range strings.Split(stored, "\n") {
		if hash == "" {
			continue
		}
		if verifyClientSecret(hash, presented) {
			return nil
		}
	}
	return errors.New("oidc: the client secret does not verify")
}

// verifyClientSecret answers whether one stored hash matches the
// presented value, by the algorithm the entry names.
func verifyClientSecret(stored, presented string) bool {
	if storedAlgorithm(stored) == "sha256" {
		digest := sha256.Sum256([]byte(presented))
		return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(digest[:])), []byte(stored)) == 1
	}
	ok, _ := crypto.NewPasswordHasher().Verify(presented, stored)
	return ok
}

// storedAlgorithm names the algorithm a stored hash carries. A `sha256`
// hash is exactly 64 lowercase hex characters; anything else is PHC.
func storedAlgorithm(stored string) string {
	if len(stored) == 64 {
		_, err := hex.DecodeString(stored)
		if err == nil {
			return "sha256"
		}
	}
	return "phc"
}

// groupIDs are the UUIDs behind an account view's memberships.
func groupIDs(view user.UserView) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(view.Groups))
	for _, group := range view.Groups {
		id, err := user.UUIDFromWire(group.ID)
		if err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}
