package devicelogin

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/riipandi/saka/framework/webutil"
)

// The cookie the creating browser holds: the request id and the pairing
// token ride it, so the exchange proves the same browser that created
// the request is the one polling for it.
const pairingCookie = "saka_device_pairing"

// handler serves the device side: the two endpoints a browser that
// cannot sign itself in reaches without a credential.
type handler struct {
	service *Service
}

func newHandler(service *Service) *handler {
	return &handler{service: service}
}

// Mount registers the device side's REST surface. Both routes sit
// outside the bearer group — the creating browser holds no token, that
// being the point of the feature.
func (h *handler) Mount(r chiRouter) {
	r.Post("/api/device-login/requests", h.create)
	r.Post("/api/device-login/requests/{id}/exchange", h.exchange)
}

// chiRouter is the registration surface the transport's router offers.
type chiRouter interface {
	Post(pattern string, handlerFn http.HandlerFunc)
}

// create opens one pairing request: the code renders on another
// device's screen, the pairing cookie stays with the creating browser.
func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	token, err := NewDeviceToken()
	if err != nil {
		webutil.Fail(w, r, http.StatusInternalServerError, "the pairing request could not be created")
		return
	}

	view, err := h.service.Create(r.Context(), token, remoteIP(r), r.UserAgent())
	switch {
	case errors.Is(err, ErrTooManyPendingRequests):
		webutil.Fail(w, r, http.StatusTooManyRequests, "this browser already holds the maximum number of pairing requests")
		return
	case err != nil:
		webutil.Fail(w, r, http.StatusInternalServerError, "the pairing request could not be created")
		return
	}

	// The cookie is http-only and lives exactly as long as the request.
	// SameSite lax keeps the exchange a same-site post, the shape a
	// fetch from the device page is. Secure rides always: the modern
	// browsers grant http://localhost the secure context the
	// development run pairs over, and a real deployment is https.
	http.SetCookie(w, &http.Cookie{
		Name:     pairingCookie,
		Value:    view.ID + "." + token,
		Path:     "/api/device-login",
		Expires:  view.ExpiresAt,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	webutil.WriteJSON(w, http.StatusCreated, view)
}

// exchange long-polls the decision. The creating browser holds the
// route; the pairing cookie proves it created the request. A turn
// inside the window answers 202 and the device spins again; the
// approval answers the account view the session will mint.
func (h *handler) exchange(w http.ResponseWriter, r *http.Request) {
	id, token, ok := readPairingCookie(r)
	if !ok {
		webutil.Fail(w, r, http.StatusUnauthorized, "the pairing cookie is missing")
		return
	}

	deadline := time.Now().Add(longPollDuration)
	for {
		outcome, err := h.service.Exchange(r.Context(), id, token)
		switch {
		case err == nil && outcome.Status == ExchangeDone:
			account, loadErr := h.service.LoadAccount(r.Context(), outcome.UserID)
			if loadErr != nil {
				webutil.Fail(w, r, http.StatusUnauthorized, "the approved account cannot sign in")
				return
			}
			webutil.WriteJSON(w, http.StatusOK, account)
			return
		case errors.Is(err, ErrCodeUnknown):
			webutil.Fail(w, r, http.StatusUnauthorized, "the pairing request is unknown, expired, or already used")
			return
		case err != nil:
			webutil.Fail(w, r, http.StatusInternalServerError, "the exchange failed")
			return
		}

		if time.Now().After(deadline) {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		select {
		case <-time.After(time.Duration(pollInterval) * time.Second):
		case <-r.Context().Done():
			return
		}
	}
}

// readPairingCookie splits the cookie's id.token pair.
func readPairingCookie(r *http.Request) (string, string, bool) {
	cookie, err := r.Cookie(pairingCookie)
	if err != nil {
		return "", "", false
	}
	id, token, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return "", "", false
	}
	return id, token, true
}

// remoteIP answers the request's remote address without its port.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
