// Package webhook is the webhook area: the outbound event surface saka
// carries and Pocket ID does not.
//
// It is an area of its own rather than a feature of identity because a
// delivery endpoint is not an account fact — it is a destination the
// deployment streams its events to, and the events it streams are the audit
// catalog's. The area is saka-only, and the surface that manages the
// endpoints is administrative all the way through.
//
// The emission seam rides the audit recorder: the recorder carries a sink
// every written record is offered to, and this area's service is that sink,
// wired after construction so internal/audit never learns this package
// exists. The delivery engine is a queue task; its processor lives in
// internal/jobs beside the other processors and resolves this service at
// task-run time.
package webhook

import (
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/framework/kernel"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/fetcher"
	"github.com/riipandi/saka/internal/queue"
	"github.com/riipandi/saka/pkg/crypto"
)

// Package registers the service this area owns, and arms the audit
// recorder's emission sink with it.
//
// The sink is wired inside the provider rather than beside it: the recorder
// is an infrastructure service this area must not depend on to construct —
// but the emission needs it, and a provider that resolved it directly would
// order the two around each other. Resolving it here constructs it on the
// way to this service, which is what the wiring intends.
var Package = do.Package(
	do.Lazy(func(i do.Injector) (*Service, error) {
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		client := do.MustInvoke[*queue.Client](i)
		fetch := do.MustInvoke[*fetcher.Client](i)
		log := do.MustInvoke[*slog.Logger](i)
		c := do.MustInvoke[*config.Config](i)

		// The signing secrets are application material, not auth material: a
		// rotation of AUTH_SECRET_KEY does not touch them, the way it does
		// not touch the SCIM tokens or the appconfig values.
		var cipher *crypto.Cipher
		if c.App.SecretKey != "" {
			built, err := crypto.NewCipherFromHex(c.App.SecretKey)
			if err != nil {
				return nil, err
			}
			cipher = built
		}

		service := NewService(pool, recorder, client, fetch, cipher, log, c.Webhook.AllowPrivateNetwork)
		recorder.WithSink(service)
		return service, nil
	}),
)

// Mount resolves what this area needs and builds the module the router
// mounts. It is the other half of the seam the composition root uses.
func Mount(i do.Injector) (kernel.Module, error) {
	return NewModule(do.MustInvoke[*Service](i)), nil
}
