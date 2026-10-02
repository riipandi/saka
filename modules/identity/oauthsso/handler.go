package oauthsso

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// The error codes the SPA redirect carries — the feature's own words, the
// same ones every redirect answers with so the client can render one
// surface for all of them.
const (
	// errCodeUnknownFlow is a state the callback cannot match: unknown,
	// spent, or expired.
	errCodeUnknownFlow = "unknown_flow"
	// errCodeProvider is a code exchange or an identity read the provider
	// refused.
	errCodeProvider = "provider_error"
	// errCodeInternal is everything else: the flow's own failures.
	errCodeInternal = "internal_error"
)

// handler serves the browser's half of the flow: the start route that
// hands the browser to the provider, and the callback route the provider
// hands it back to. Both sit outside the bearer group — the caller holds
// no token, that being the point of the feature — and both answer with
// redirects, never a JSON envelope: the browser here is mid-navigation,
// not a client reading a body.
type handler struct {
	service *Service
}

func newHandler(service *Service) *handler {
	return &handler{service: service}
}

// Mount registers the flow's REST routes.
func (h *handler) Mount(r chi.Router) {
	r.Get("/oauth/{provider}/start", h.start)
	r.Get("/oauth/{provider}/callback", h.callback)
}

// start hands the browser to the provider. The flow row is written before
// the redirect leaves; a refused begin is a redirect to the SPA's error
// surface, the answer the browser can act on.
func (h *handler) start(w http.ResponseWriter, r *http.Request) {
	authorizeURL, err := h.service.Begin(r.Context(), chi.URLParam(r, "provider"))
	if err != nil {
		http.Redirect(w, r, h.errorRedirect(err), http.StatusFound)
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// callback consumes what the provider returned: the code and the state
// the browser carries. The resolved identity rests on the flow row, and
// the fresh flow token rides the SPA redirect the answer is.
func (h *handler) callback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	// The provider's own error answer — the user declined, the
	// connection's client was refused — is the flow's end, not a 500.
	if providerErr := query.Get("error"); providerErr != "" {
		http.Redirect(w, r, h.service.ErrorRedirect(errCodeProvider), http.StatusFound)
		return
	}

	flowToken, err := h.service.Callback(r.Context(),
		chi.URLParam(r, "provider"), query.Get("code"), query.Get("state"))
	if err != nil {
		// The browser's answer is the redirect below; the operator's is
		// this line — a resolution the provider refused is the one
		// failure the flow cannot say out loud.
		slog.WarnContext(r.Context(), "oauthsso: the callback did not resolve",
			"error", err, "provider", chi.URLParam(r, "provider"))
		http.Redirect(w, r, h.errorRedirect(err), http.StatusFound)
		return
	}

	http.Redirect(w, r, h.service.FlowRedirect(flowToken), http.StatusFound)
}

// errorRedirect maps the service's failures onto the SPA surface's error
// codes.
func (h *handler) errorRedirect(err error) string {
	switch {
	case errors.Is(err, ErrFlowUnknown), errors.Is(err, ErrConnectionUnavailable):
		return h.service.ErrorRedirect(errCodeUnknownFlow)
	case errors.Is(err, ErrResolutionFailed), errors.Is(err, ErrIdentityInvalid):
		return h.service.ErrorRedirect(errCodeProvider)
	default:
		return h.service.ErrorRedirect(errCodeInternal)
	}
}
