package oidc

import (
	"fmt"
	"net/http"
	"strings"

	"uuid"

	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/luikyv/go-oidc/pkg/goidc"

	"github.com/riipandi/saka/modules/identity/user"
)

// The JOSE type member RFC 9068 names for JWT access tokens. The provider
// mints access tokens with it and ID tokens without any type member, so it
// is the one marker that tells an access-token hint from an ID-token hint
// — the hint must be an ID token, and the library's own validation reads
// only the claims, which both token kinds carry.
const accessTokenTypeMember = "at+jwt"

// auditEventSessionEnded mirrors audit.EventOidcSessionEnded without
// pulling the audit package into every protocol test.
const auditEventSessionEnded = "oidc_session_ended"

// logoutPolicy is the one policy every RP-initiated logout walks. The
// setup requires an ID-token hint: the provider holds no browser session,
// so a request without one names no subject and nothing can be revoked.
// The logout verifies the hint is an ID token, then withdraws the
// subject's grants and tokens for the client in one transaction.
func logoutPolicy(service *Service) goidc.LogoutPolicy {
	return goidc.NewLogoutPolicy("saka-end-session",
		func(_ *http.Request, session *goidc.LogoutSession) bool {
			return session.IDTokenHint != ""
		},
		func(w http.ResponseWriter, req *http.Request, session *goidc.LogoutSession) (goidc.Status, error) {
			typ, err := joseTypeMember(session.IDTokenHint)
			if err != nil {
				return goidc.StatusFailure, goidc.WrapError(goidc.ErrorCodeInvalidRequest, "invalid request",
					fmt.Errorf("id_token_hint is not a readable token: %w", err))
			}
			if strings.EqualFold(typ, accessTokenTypeMember) {
				return goidc.StatusFailure, goidc.WrapError(goidc.ErrorCodeInvalidRequest, "invalid request",
					fmt.Errorf("id_token_hint must be an ID token, not an access token"))
			}

			claims := session.IDTokenHintClaims
			if claims == nil || claims.Subject == "" {
				return goidc.StatusFailure, goidc.WrapError(goidc.ErrorCodeInvalidRequest, "invalid request",
					fmt.Errorf("id_token_hint carries no subject"))
			}

			// The provider mints the ID token's subject in the raw UUID form
			// the grant rows store; a pairwise subject would name neither
			// form the ledger knows, and fails the lookup below.
			userID, err := uuid.Parse(claims.Subject)
			if err != nil {
				wire, wireErr := user.UUIDFromWire(claims.Subject)
				if wireErr != nil {
					return goidc.StatusFailure, goidc.WrapError(goidc.ErrorCodeInvalidRequest, "invalid request",
						fmt.Errorf("id_token_hint names an unknown subject"))
				}
				userID = wire
			}

			// The hint's sid names the OP session the RP correlates. The
			// provider mints the member as the grant's own identifier —
			// the correlation the back-channel delivery carries — and a
			// third-party hint's answer rides through so the client's
			// session matching keeps working.
			sid := hintSessionID(claims)

			if err := service.EndSession(req.Context(), userID.String(), session.ClientID, sid); err != nil {
				return goidc.StatusFailure, fmt.Errorf("could not end the session: %w", err)
			}
			// The browser-session marker dies with the session it names:
			// a later prompt=none request must answer login_required, not
			// resume an authentication the user just ended.
			clearBrowserSessionCookie(w)
			return goidc.StatusSuccess, nil
		},
	)
}

// hintSessionID reads the session identifier a hint's claims carry, when
// one does. The delivery's sid is the RP's correlation, not saka's —
// the row's identity never names it.
func hintSessionID(claims *goidc.IDToken) string {
	if claims == nil {
		return ""
	}
	raw, ok := claims.AdditionalClaims["sid"].(string)
	if !ok {
		return ""
	}
	return raw
}

// clearBrowserSessionCookie expires the OP's browser-session marker in
// the response that answers the logout — the mirror of the writes the
// completion paths make (protocol_policy.go).
func clearBrowserSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcSessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// joseTypeMember reads the type member of the first JOSE header of a
// compact serialization. An empty answer is normal: the provider mints ID
// tokens without one.
func joseTypeMember(token string) (string, error) {
	message, err := jws.Parse([]byte(token))
	if err != nil {
		return "", err
	}
	if len(message.Signatures()) == 0 {
		return "", fmt.Errorf("the token carries no signature header")
	}
	typ, _ := message.Signatures()[0].ProtectedHeaders().Type()
	return typ, nil
}

// defaultPostLogout sends a browser that named no registered
// post_logout_redirect_uri back to the SPA, where it decides what a
// signed-out visitor sees.
func defaultPostLogout(w http.ResponseWriter, req *http.Request, _ *goidc.LogoutSession) error {
	http.Redirect(w, req, "/", http.StatusFound)
	return nil
}
