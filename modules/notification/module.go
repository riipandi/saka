// Package notification is the notification area: what an administrator
// announces and what an account reads.
//
// It is an area of its own rather than a feature of identity because a
// notification is not an account fact — it is a message the deployment
// publishes to an audience the administrator draws, and the surface that
// manages it is administrative while the surface that reads it is every
// account's.
//
// The area owns its own wiring, like every other: the registry names it
// and knows nothing about its service. The email pass is wired
// post-construction over the durable queue — the service states that a
// notification asked for an email pass, the dispatcher decides how the
// message travels and whether the deployment pays for it — because a
// provider cannot take the queue without ordering the area's construction
// around it.
package notification

import (
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/kernel"
	"github.com/riipandi/tango/internal/queue"
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

		// The email pass rides the queue when the container holds one. A
		// consumer area served without a queue keeps its notifications
		// in-app only, which is a smaller feature rather than a broken
		// one; a container that names no configuration is a wiring defect
		// the must-invoke reports.
		if client, err := do.Invoke[*queue.Client](i); err == nil && client != nil {
			c := do.MustInvoke[*config.Config](i)
			service.WithEmailDispatcher(NewEmailDispatcher(
				client, log, c.Mailer.Notifications.AnnouncementEmailEnabled,
			))
		}
		return service, nil
	}),
)

// Mount resolves what this area needs and builds the module the router
// mounts. It is the other half of the seam the composition root uses.
func Mount(i do.Injector) (kernel.Module, error) {
	return NewModule(do.MustInvoke[*Service](i)), nil
}
