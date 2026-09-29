package scimsync

import (
	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"

	federationv1connect "github.com/riipandi/tango/codegen/proto/go/tango/federation/v1/federationv1connect"
)

// ModuleName is the name this feature reports under. The area it belongs
// to qualifies it, so the name is the feature alone.
const ModuleName = "scimsync"

// Module serves the SCIM provider procedures: the RPC surface, all of it.
// Provisioning is an operator's configuration and a queue's pass; no
// browser form calls it, so there is nothing on the HTTP router to claim.
type Module struct {
	service *Service
}

// NewModule builds the module over the service.
func NewModule(service *Service) *Module {
	return &Module{service: service}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the module's endpoints. The surface is RPC alone, so
// there is nothing on the HTTP router to claim.
func (m *Module) Mount(r chi.Router) {}

// MountRPC registers the procedures on the RPC router. Each procedure is
// registered at its own path: the generated handler answers a path under
// its prefix it does not know with a plain-text 404, which a Connect
// client cannot read.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	_, handler := federationv1connect.NewScimProviderServiceHandler(newRPCHandler(m.service), opts...)
	r.Handle(federationv1connect.ScimProviderServiceGetByClientProcedure, handler)
	r.Handle(federationv1connect.ScimProviderServiceCreateProcedure, handler)
	r.Handle(federationv1connect.ScimProviderServiceUpdateProcedure, handler)
	r.Handle(federationv1connect.ScimProviderServiceDeleteProcedure, handler)
	r.Handle(federationv1connect.ScimProviderServiceSyncProcedure, handler)
}
