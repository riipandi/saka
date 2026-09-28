package appconfig

import (
	"net/http"

	"github.com/riipandi/tango/pkg/jwtutils"
	"github.com/riipandi/tango/pkg/responder"
)

// serveConfiguration answers `GET /api/configuration`: the deployment's
// resolved configuration, the JSON file's view of the world.
//
// The scope follows the caller. Without a credential the endpoint answers
// the public subset — the facts a login screen shows. With an
// administrator's bearer token it answers every non-secret setting the
// process runs on. The route is public on the guard's books, so the
// middleware authenticates opportunistically: a token is verified when
// presented, and an anonymous caller is never refused. The scope widening
// here is not a gate — the endpoint answers to everyone — which is why the
// check reads the caller's own claims instead of a rule in the tables.
//
// The secrets are absent from the published document by construction, in
// either scope. The configuration is resolved once at startup, so the
// answer describes the running process, not the file on disk.
func (m *Module) serveConfiguration(w http.ResponseWriter, r *http.Request) {
	caller, _ := jwtutils.CallerFrom(r.Context())

	responder.Success(w, r, http.StatusOK, m.config.Published(caller.IsAdministrator()),
		responder.WithMessage("the application configuration"))
}
