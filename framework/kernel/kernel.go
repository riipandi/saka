// Package kernel holds the contract every feature module implements, so the
// transport can serve a route without knowing which module owns it.
package kernel

import (
	"fmt"

	"connectrpc.com/connect"

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
	// MountRPC registers the module's procedures on the RPC router. The router
	// is mounted with the RPC prefix stripped, so the paths are the procedure
	// paths a generated Connect handler answers. MountRPC runs once, before the
	// listener opens.
	//
	// The handler options are the transport's, and a module passes them to every
	// generated handler it registers. They carry the shared JSON codec — the one
	// that serializes snake_case, so a procedure answers in the same field names
	// its REST twin writes — and the panic boundary. A module that registers a
	// handler without them answers in protobuf's default camelCase instead.
	MountRPC(r chi.Router, opts ...connect.HandlerOption)
}

// MountRPC registers the procedures of every module that serves any. A module
// without procedures is skipped, so one list can carry both kinds.
//
// Like Mount, the procedures are read back from a scratch router first, so two
// modules registering one procedure path fails the run naming both — the
// generated handler would otherwise answer whichever registered last.
func MountRPC(r chi.Router, opts []connect.HandlerOption, modules ...Module) {
	claims := map[string]string{}
	for _, module := range modules {
		rpc, ok := module.(RPCModule)
		if !ok {
			continue
		}
		// The path alone is the claim: the generated handler answers its
		// procedure path for every method, so two modules on one path conflict
		// however the methods read back.
		scratch := chi.NewRouter()
		rpc.MountRPC(scratch, opts...)
		for _, route := range scratch.Routes() {
			if owner, taken := claims[route.Pattern]; taken {
				panic(fmt.Sprintf("kernel: module %q claims procedure %s already claimed by module %q",
					module.Name(), route.Pattern, owner))
			}
			claims[route.Pattern] = module.Name()
		}
		rpc.MountRPC(r, opts...)
	}
}
