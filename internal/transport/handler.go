package transport

import (
	"net/http"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/pkg/responder"
)

// apiRoot is the /api landing endpoint. It names what is served, so a probe
// that reached the API can report the surface it found without reading docs.
func apiRoot(cfg config.Config) http.HandlerFunc {
	type apiRootData struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Mode    string `json:"mode"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		responder.Success(w, r, http.StatusOK, apiRootData{
			Name:    config.AppIdentifier,
			Version: config.AppVersion,
			Mode:    cfg.App.Mode,
		})
	}
}
