//go:build !debug

// The release counterpart of the devtool: the paths are claimed so a request
// is refused with the envelope protocol rather than answered by the SPA, but
// nothing is served (devtool_debug.go holds the debug build's surface).

package devtools

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"

	"github.com/riipandi/saka/framework/webutil"
)

func Mount(r chi.Router, _ do.Injector) {
	r.Get(devtoolUIPath, devtoolUnavailable)
	r.Get(devtoolUIPath+"/*", devtoolUnavailable)
	r.Post("/debug/encode-id", devtoolUnavailable)
	r.Post("/debug/decode-id", devtoolUnavailable)
	r.Get(webauthnProbePath, devtoolUnavailable)
	r.Get("/debug/file-upload", devtoolUnavailable)
}

func devtoolUnavailable(w http.ResponseWriter, r *http.Request) {
	webutil.NotFoundJSON(w, r)
}
