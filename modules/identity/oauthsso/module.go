package oauthsso

import (
	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	authnv1connect "github.com/riipandi/tango/codegen/proto/go/tango/authn/v1/authnv1connect"
)

// ModuleName names the feature in composition reports and logs.
const ModuleName = "oauthsso"

// Module serves the OAuth SSO feature. The connection procedures ride the
// RPC surface today; the flow's REST routes join the mount in the flow
// phase, on this module's own router.
type Module struct {
	service    *Service
	rpcHandler *rpcHandler
}

// NewModule builds the module over the connection service.
func NewModule(service *Service) *Module {
	return &Module{service: service, rpcHandler: newRPCHandler(service)}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the feature's REST routes. The flow's start and callback
// are the routes a browser crosses mid-redirect; they join in the flow
// phase, and until then the feature mounts nothing on the application
// router.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the OAuth SSO procedures on the RPC router. The
// handler options are the transport's — the shared snake_case codec and
// the panic boundary — so the procedures answer exactly like the rest.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, connectHandler := authnv1connect.NewOAuthSSOServiceHandler(m.rpcHandler, opts...)
	r.Handle(authnv1connect.OAuthSSOServiceBeginSignInProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceContinueSignInProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceListConnectionsProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceGetConnectionProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceCreateConnectionProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceUpdateConnectionProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceDeleteConnectionProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceListLinkedConnectionsProcedure, connectHandler)
	r.Handle(authnv1connect.OAuthSSOServiceUnlinkConnectionProcedure, connectHandler)
}
