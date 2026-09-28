package oidc

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"uuid"

	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/jwtutils"
)

// The two authentication policies the authorization endpoint walks: the
// sign-in policy answers the browser's first visit, the interaction policy
// answers the SPA's callback with the credential and the consent decision.
const (
	policySignInID       = "tango-sign-in"
	policyInteractionID  = "tango-interaction"
	consentBodyByteLimit = 8192
)

// consentDecision is the JSON body the SPA posts to the callback: the
// scopes the account agreed to hand the client, when it agreed at all.
type consentDecision struct {
	Scopes []string `json:"scopes,omitempty"`
}

// signInPolicy runs while the browser stands on the authorization
// endpoint itself. Without a credential it redirects to the SPA's
// interaction page — the provider keeps the session pending and writes
// nothing, so the redirect here is the whole response. With one, the
// decision proceeds on the spot.
func signInPolicy(service *Service) goidc.AuthnPolicy {
	return goidc.NewPolicy(policySignInID,
		func(r *http.Request, _ *goidc.AuthnSession, _ *goidc.Client) bool {
			return r.URL.Path == protocolAuthorizeEndpoint
		},
		func(w http.ResponseWriter, r *http.Request, session *goidc.AuthnSession, client *goidc.Client) (goidc.Status, error) {
			caller, ok := jwtutils.CallerFrom(r.Context())
			if !ok {
				interactionRedirect(w, r, protocolAuthorizeEndpoint, session.ID)
				return goidc.StatusPending, nil
			}
			return completeAuthentication(r.Context(), service, w, r, session, client, caller)
		})
}

// interactionPolicy runs on the callback path the SPA posts to.
func interactionPolicy(service *Service) goidc.AuthnPolicy {
	return goidc.NewPolicy(policyInteractionID,
		func(r *http.Request, _ *goidc.AuthnSession, _ *goidc.Client) bool {
			return strings.HasPrefix(r.URL.Path, protocolAuthorizeEndpoint+"/")
		},
		func(w http.ResponseWriter, r *http.Request, session *goidc.AuthnSession, client *goidc.Client) (goidc.Status, error) {
			caller, ok := jwtutils.CallerFrom(r.Context())
			if !ok {
				interactionRedirect(w, r, protocolAuthorizeEndpoint, session.ID)
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
	if view.SkipConsent {
		session.GrantedScopes = strings.Join(requestedScopes(session), " ")
		session.Subject = account.subject
		session.Username = account.username
		return goidc.StatusSuccess, nil
	}

	if approved, decided := decisionScopes(r, requestedScopes(session)); decided {
		session.GrantedScopes = strings.Join(approved, " ")
		session.Subject = account.subject
		session.Username = account.username
		if err := service.recordAuthorization(ctx, account.subject, client.ID, approved); err != nil {
			return goidc.StatusFailure, err
		}
		return goidc.StatusSuccess, nil
	}

	// No decision in the body: a consent the account already gave that
	// covers the request ends the question; otherwise the SPA answers
	// with the interaction document and the session stays pending.
	if known, err := service.authorizedScopes(ctx, account.subject, client.ID); err == nil && coversScopes(known, requestedScopes(session)) {
		session.GrantedScopes = strings.Join(requestedScopes(session), " ")
		session.Subject = account.subject
		session.Username = account.username
		return goidc.StatusSuccess, nil
	}
	writeInteraction(w, http.StatusOK, map[string]any{
		"interaction_id":   session.ID,
		"client_id":        client.ID,
		"client_name":      client.Name,
		"requested_scopes": requestedScopes(session),
		"consent_required": true,
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
// hash the client's credentials document carries. The hashes are PHC
// strings the password hasher reads.
func clientSecretVerifier(ctx context.Context, stored, presented string) error {
	hasher := crypto.NewPasswordHasher()
	for _, hash := range strings.Split(stored, "\n") {
		if hash == "" {
			continue
		}
		if ok, _ := hasher.Verify(presented, hash); ok {
			return nil
		}
	}
	return errors.New("oidc: the client secret does not verify")
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
