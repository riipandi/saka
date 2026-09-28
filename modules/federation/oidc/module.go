package oidc

import (
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	federationv1connect "github.com/riipandi/tango/codegen/proto/go/tango/federation/v1/federationv1connect"
	"github.com/riipandi/tango/pkg/responder"
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
	r.Get("/api/oidc/clients/{id}/logo", func(w http.ResponseWriter, r *http.Request) {
		logo, err := m.service.Logo(r.Context(), chi.URLParam(r, "id"))
		switch {
		case errors.Is(err, ErrClientNotFound):
			responder.Fail(w, r, http.StatusNotFound, "client not found")
			return
		case errors.Is(err, ErrLogoMissing):
			responder.Fail(w, r, http.StatusNotFound, "the client has no logo")
			return
		case errors.Is(err, ErrLogosUnavailable):
			responder.Fail(w, r, http.StatusServiceUnavailable, "logo storage is not available")
			return
		case err != nil:
			responder.WriteError(w, r, err)
			return
		}
		defer logo.Body.Close()

		// The key names one client's logo and its content changes on an
		// update, so the cache holds briefly rather than forever.
		w.Header().Set("Content-Type", logo.ContentType)
		w.Header().Set("Cache-Control", "private, max-age=60")
		if _, err := io.Copy(w, logo.Body); err != nil {
			responder.WriteError(w, r, err)
		}
	})
}

// MountRPC registers the procedures on the RPC router. The handler options
// are the transport's — the shared snake_case codec and the panic boundary —
// so the procedures answer exactly like the transport's own. Each procedure
// is registered at its own path: the generated handler answers a path under
// its prefix it does not know with a plain-text 404, which a Connect client
// cannot read.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := federationv1connect.NewOidcClientServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(federationv1connect.OidcClientServiceListClientsProcedure, handler)
	r.Handle(federationv1connect.OidcClientServiceCreateClientProcedure, handler)
	r.Handle(federationv1connect.OidcClientServiceGetClientProcedure, handler)
	r.Handle(federationv1connect.OidcClientServiceUpdateClientProcedure, handler)
	r.Handle(federationv1connect.OidcClientServiceDeleteClientProcedure, handler)
	r.Handle(federationv1connect.OidcClientServiceUpdateAllowedUserGroupsProcedure, handler)
	r.Handle(federationv1connect.OidcClientServiceGetClientMetaProcedure, handler)
	r.Handle(federationv1connect.OidcClientServicePreviewClientProcedure, handler)
	r.Handle(federationv1connect.OidcClientServiceUploadLogoProcedure, handler)
	r.Handle(federationv1connect.OidcClientServiceDeleteLogoProcedure, handler)
	r.Handle(federationv1connect.OidcClientServiceListSecretsProcedure, handler)
	r.Handle(federationv1connect.OidcClientServiceCreateSecretProcedure, handler)
	r.Handle(federationv1connect.OidcClientServiceDeleteSecretProcedure, handler)
	r.Handle(federationv1connect.OidcClientServiceRefreshClientProcedure, handler)
}
