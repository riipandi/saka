package oauthsso

import (
	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	authnv1connect "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1/authnv1connect"
)

// ModuleName names the feature in composition reports and logs.
const ModuleName = "oauthsso"

// Module serves the OAuth SSO feature: the connection procedures, the
// flow's REST routes, and — in the phases that follow — the resolution
// and the account surfaces.
type Module struct {
	service    *Service
	rpcHandler *rpcHandler
	rest       *handler
}

// NewModule builds the module over the connection service.
func NewModule(service *Service) *Module {
	return &Module{
		service:    service,
		rpcHandler: newRPCHandler(service),
		rest:       newHandler(service),
	}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the flow's REST routes. Both sit outside the bearer
// group — the browser crossing them holds no token, that being the point
// of the feature.
func (m *Module) Mount(r chi.Router) {
	m.rest.Mount(r)
}

// MountRPC registers the OAuth SSO procedures on the RPC router. The
// handler options are the transport's — the shared snake_case codec and
// the panic boundary — so the procedures answer exactly like the rest.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, connectHandler := authnv1connect.NewOAuthSSOServiceHandler(m.rpcHandler, opts...)
	r.Handle(authnv1connect.OAuthSSOServiceBeginSignInProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceContinueSignInProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceVerifySignInEmailProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceListConnectionsProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceGetConnectionProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceCreateConnectionProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceUpdateConnectionProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceDeleteConnectionProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceListLinkedConnectionsProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceUnlinkConnectionProcedure, connectHandler)
}
