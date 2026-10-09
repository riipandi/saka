package devicelogin

import (
	"connectrpc.com/connect/v2"
	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"

	authnv1connect "github.com/riipandi/saka/codegen/proto/go/saka/authn/v1/authnv1connect"
	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/kernel"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/modules/identity/user"
)

// ModuleName names the feature in composition reports and logs. The
// procedures ride the authn area's contract, but the feature is its own:
// the pairing rows, the REST surface, and the approval procedures move
// together.
const ModuleName = "devicelogin"

// Module serves the device login feature: the REST pairing surface and
// the two approval procedures.
type Module struct {
	service    *Service
	rpcHandler *rpcHandler
}

// NewModule builds the module over the pairing service.
func NewModule(service *Service) *Module {
	return &Module{service: service, rpcHandler: newRPCHandler(service)}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the device side on the HTTP router. The two routes
// sit outside the bearer group — the creating browser holds no token.
func (m *Module) Mount(r chi.Router) {
	newHandler(m.service).Mount(r)
}

// MountRPC registers the approval procedures on the RPC server. The server
// carries the transport's interceptors and the mount the shared snake_case
// codec, so the procedures answer exactly like the rest.
func (m *Module) MountRPC(server *connect.Server) {
	authnv1connect.RegisterDeviceApprovalServiceHandler(server, m.rpcHandler)
}

// Package registers the services this feature owns.
var Package = do.Package(
	do.Lazy(func(i do.Injector) (*Service, error) {
		c := do.MustInvoke[*config.Config](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		users := do.MustInvoke[*user.Service](i)
		recorder := do.MustInvoke[*fwaudit.Recorder](i)
		return NewService(pool, users, recorder, c.App.BaseURL), nil
	}),

	do.Lazy(func(i do.Injector) (kernel.Module, error) {
		service := do.MustInvoke[*Service](i)
		return NewModule(service), nil
	}),
)
