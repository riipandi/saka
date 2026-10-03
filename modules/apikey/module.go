// Package apikey is the API key area: the machine credentials an account
// issues for its own scripting and integrations.
//
// It is an area of its own rather than a feature of identity because the
// credential is not an account fact — it is a credential a transport
// authenticates with, the way a session is, and the surface that manages it
// is the one surface such a credential is refused on.
//
// The area owns its own wiring, like every other: the registry names it and
// knows nothing about its service. The transport's authenticator is the one
// consumer outside the area the credential has, and it resolves through the
// container like any other consumer — the registration lives here, with the
// service.
package apikey

import (
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/kernel"
)

// Package registers the service this area owns.
//
// The composition root applies it while the container is built, so it only
// registers: the service is constructed when something resolves it.
var Package = do.Package(
	do.Lazy(func(i do.Injector) (*Service, error) {
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		log := do.MustInvoke[*slog.Logger](i)
		return NewService(pool, recorder, log), nil
	}),
)

// Mount resolves what this area needs and builds the module the router
// mounts. It is the other half of the seam the composition root uses.
func Mount(i do.Injector) (kernel.Module, error) {
	return NewModule(do.MustInvoke[*Service](i)), nil
}
