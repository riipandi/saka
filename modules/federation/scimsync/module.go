package scimsync

import (
	"connectrpc.com/connect/v2"
	"github.com/go-chi/chi/v5"

	federationv1connect "github.com/riipandi/saka/codegen/proto/go/saka/federation/v1/federationv1connect"
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

// MountRPC registers the procedures on the RPC server. The server carries the
// transport's interceptors and the mount the shared snake_case codec, so the
// procedures answer exactly like the transport's own.
func (m *Module) MountRPC(server *connect.Server) {
	federationv1connect.RegisterScimProviderServiceHandler(server, newRPCHandler(m.service))
}
