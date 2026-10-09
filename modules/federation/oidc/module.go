package oidc

import (
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect/v2"
	"github.com/go-chi/chi/v5"

	federationv1connect "github.com/riipandi/saka/codegen/proto/go/saka/federation/v1/federationv1connect"
	"github.com/riipandi/saka/framework/webutil"
)

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "oidc"

// Module serves the OIDC client management: the RPC procedures, plus the one
// REST route the sign-in page needs — the logo, which an <img> tag fetches
// without a protocol.
type Module struct {
	service *Service
}

// NewModule builds the module over the client service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the module's REST route. The logo read is public on the
// guard's books — a browser fetches it before any credential exists — so the
// unknown-client and no-logo states are refusals the handler itself states,
// in the envelope the REST surface answers.
func (m *Module) Mount(r chi.Router) {
	r.Get("/oidc/clients/{id}/logo", func(w http.ResponseWriter, r *http.Request) {
		logo, err := m.service.Logo(r.Context(), chi.URLParam(r, "id"))
		switch {
		case errors.Is(err, ErrClientNotFound):
			webutil.Fail(w, r, http.StatusNotFound, "client not found")
			return
		case errors.Is(err, ErrLogoMissing):
			webutil.Fail(w, r, http.StatusNotFound, "the client has no logo")
			return
		case errors.Is(err, ErrLogosUnavailable):
			webutil.Fail(w, r, http.StatusServiceUnavailable, "logo storage is not available")
			return
		case err != nil:
			webutil.WriteError(w, r, err)
			return
		}
		defer logo.Body.Close()

		// The key names one client's logo and its content changes on an
		// update, so the cache holds briefly rather than forever.
		w.Header().Set("Content-Type", logo.ContentType)
		w.Header().Set("Cache-Control", "private, max-age=60")
		if _, err := io.Copy(w, logo.Body); err != nil {
			webutil.WriteError(w, r, err)
		}
	})
}

// MountRPC registers the procedures on the RPC server. The server carries the
// transport's interceptors and the mount the shared snake_case codec, so the
// procedures answer exactly like the transport's own.
func (m *Module) MountRPC(server *connect.Server) {
	federationv1connect.RegisterOidcClientServiceHandler(server, newRPCHandler(m.service))
	federationv1connect.RegisterOidcConsentServiceHandler(server, newConsentHandler(m.service))
}
