// Package kernel holds the contract every feature module implements, so the
// transport can serve a route without knowing which module owns it.
package kernel

import (
	"fmt"

	"connectrpc.com/connect/v2"

	"github.com/go-chi/chi/v5"
)

// Module is one feature slice the server mounts. A module owns its package
// (schema, repository, service, handlers) and registers its endpoints through
// Mount, so no route list outside the module can drift from its handlers.
type Module interface {
	// Name reports the module in composition reports and logs. It is
	// diagnostic: routes are mounted in registration order, not by name.
	Name() string
	// Mount registers the module's endpoints on the router. Mount runs once at
	// startup, before the listener opens, so a registration failure is a
	// construction failure of the server, not a 500 on the first request.
	Mount(r chi.Router)
}

// Mount registers every module on the router in the order given.
//
// Each module's routes are read back from a scratch router before they are
// registered on the real one, so two modules claiming the same pattern fails
// the run naming both, instead of chi's last-wins registration handing the
// route to whichever module mounted later. Mounting twice is safe: Mount is
// registration only, so it has no effect beyond the handlers it registers.
// A conflict inside one module's own registration is chi's last-wins and is
// the module's own defect; the boundary policed here is between modules.
func Mount(r chi.Router, modules ...Module) {
	claims := map[string]string{}
	for _, module := range modules {
		name := module.Name()
		scratch := chi.NewRouter()
		module.Mount(scratch)
		for _, route := range scratch.Routes() {
			for method := range route.Handlers {
				key := method + " " + route.Pattern
				if owner, taken := claims[key]; taken {
					panic(fmt.Sprintf("kernel: module %q claims route %s already claimed by module %q",
						name, key, owner))
				}
				claims[key] = name
			}
		}
		module.Mount(r)
	}
}

// RPCModule is a module that also serves ConnectRPC procedures.
//
// It is a separate interface rather than a second method on Module, so a module
// that serves no procedure says so by not implementing it: the transport asks
// with a type assertion instead of calling a method that would have to do
// nothing.
type RPCModule interface {
	Module
	// MountRPC registers the module's procedures on the RPC server. The server
	// is the transport's: it carries the shared interceptors — the guard, the
	// contract enforcement, the panic boundary, the telemetry — and the mount
	// carries the shared JSON codec, the one that serializes snake_case, so a
	// procedure answers in the same field names its REST twin writes. MountRPC
	// runs once, before the listener opens.
	MountRPC(server *connect.Server)
}

// MountRPC registers the procedures of every module that serves any. A module
// without procedures is skipped, so one list can carry both kinds.
//
// Like Mount, the procedures are read back from a scratch server first, so two
// modules registering one procedure fails the run naming both — the server's
// own duplicate detection would otherwise answer whichever registered last.
func MountRPC(server *connect.Server, modules ...Module) {
	claims := map[string]string{}
	for _, module := range modules {
		rpc, ok := module.(RPCModule)
		if !ok {
			continue
		}
		// The procedure path alone is the claim: it names the service and the
		// method, so two modules on one procedure conflict outright.
		scratch := connect.NewServer()
		rpc.MountRPC(scratch)
		for spec := range scratch.Specs() {
			if owner, taken := claims[spec.Procedure]; taken {
				panic(fmt.Sprintf("kernel: module %q claims procedure %s already claimed by module %q",
					module.Name(), spec.Procedure, owner))
			}
			claims[spec.Procedure] = module.Name()
		}
		rpc.MountRPC(server)
	}
}
