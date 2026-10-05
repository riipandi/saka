// Package auditlog is the audit-log area: the read surface over the records
// the application writes about itself.
//
// It is an area of its own rather than a feature of identity because it
// belongs to no feature: sign-in, sign-up, account administration, and
// verification all write records, and the area that reads them is the one
// place that answers "what happened" across all of them. The writer is not
// here — internal/audit owns it, so a feature can record without importing
// this area and a module can be swapped without touching what it describes.
//
// The area owns its own wiring, like every other: the registry names it and
// knows nothing about its service.
package auditlog

import (
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/kernel"
	"github.com/riipandi/saka/internal/config"
)

// ModuleName is the name the area reports under.
const ModuleName = "auditlog"

// Deps are the resolved services the area is built from.
type Deps struct {
	// Service reads the records.
	Service *Service
	// Logger is the process logger the module's diagnostics write through.
	Logger *slog.Logger
}

// Package registers the services this area owns.
//
// The composition root applies it while the container is built, so it only
// registers: the service is constructed when something resolves it.
var Package = do.Package(
	do.Lazy(func(i do.Injector) (*Service, error) {
		pool := do.MustInvoke[*datastore.Postgres](i)
		log := do.MustInvoke[*slog.Logger](i)
		return NewService(pool, NewRepository(), log), nil
	}),
)

// Mount resolves what this area needs and builds the module the router
// mounts. It is the other half of the seam the composition root uses.
func Mount(i do.Injector) (kernel.Module, error) {
	// The configuration is resolved so an unusable one fails the run here
	// rather than on the first request, the same reason every other area
	// resolves what it needs at this point.
	_ = do.MustInvoke[*config.Config](i)

	return NewModule(
		do.MustInvoke[*Service](i),
		do.MustInvoke[*slog.Logger](i),
	), nil
}
