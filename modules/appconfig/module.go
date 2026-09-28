// Package appconfig is the application-configuration area: the deployment's
// own settings surface.
//
// It is an area of its own rather than a feature of identity because the
// settings it serves are the deployment's, not any account's — the caller is
// always an administrator acting on the application itself.
//
// The area owns no tables today. The one procedure it serves reads the
// caller's account through the identity area's user package, the way the
// audit-log reader does; a configuration store of its own arrives with the
// configuration read and update procedures, which are still planned.
//
// The area owns its own wiring, like every other: the registry names it and
// knows nothing about its service.
package appconfig

import (
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/kernel"
	"github.com/riipandi/tango/internal/mailer"
)

// Package registers the service this area owns.
//
// The composition root applies it while the container is built, so it only
// registers: the service is constructed when something resolves it. The
// mailer is the infrastructure the registry's prewarm walk resolves, so a
// process that reaches the listener has it.
var Package = do.Package(
	do.Lazy(func(i do.Injector) (*Service, error) {
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		mail := do.MustInvoke[*mailer.Service](i)
		log := do.MustInvoke[*slog.Logger](i)
		return NewService(pool, recorder, mail, log), nil
	}),
)

// Mount resolves what this area needs and builds the module the router
// mounts. It is the other half of the seam the composition root uses.
func Mount(i do.Injector) (kernel.Module, error) {
	return NewModule(do.MustInvoke[*Service](i)), nil
}
