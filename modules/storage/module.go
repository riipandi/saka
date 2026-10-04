// Package storage is the storage area: the buckets the file engine serves
// and the surface that manages them.
//
// It is an area of its own rather than a feature of another area because a
// bucket is not an account fact — it is a deployment-wide namespace the
// engine, the serving route, and every uploading feature share, and the
// surface that manages it is administrative all the way through.
//
// The area owns its own wiring, like every other: the registry names it and
// knows nothing about its service. The default-bucket setting's reader is
// wired post-construction over the appconfig feature — the service states
// that a deletion asks for the setting, the settings feature decides how
// the value is stored — because a provider that took the dependency would
// order the areas' construction around it.
package storage

import (
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/kernel"
	"github.com/riipandi/saka/framework/storage"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/modules/appconfig"
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
		service := NewService(pool, recorder, log)

		// A nil settings feature keeps the engine's built-in default — the
		// state a bare wiring is in; the composition root always resolves
		// the catalog.
		if settings, err := do.Invoke[*appconfig.Settings](i); err == nil && settings != nil {
			service.WithSettings(settings)
		}
		// The engine is infrastructure: the container provisioning rides it,
		// and a wiring without one creates the row alone.
		if manager, err := do.Invoke[*storage.Manager](i); err == nil && manager != nil {
			service.WithEngine(manager)
		}
		return service, nil
	}),
)

// Mount resolves what this area needs and builds the module the router
// mounts. It is the other half of the seam the composition root uses.
func Mount(i do.Injector) (kernel.Module, error) {
	return NewModule(do.MustInvoke[*Service](i)), nil
}
