package handler

import (
	"net/http"

	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/internal/config"
)

// apiRoot is the /api landing endpoint. It names what is served, so a probe
// that reached the API can report the surface it found without reading docs.
func APIRoot(cfg config.Config) http.HandlerFunc {
	type apiRootData struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Mode    string `json:"mode"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		webutil.Success(w, r, http.StatusOK, apiRootData{
			Name:    config.AppIdentifier,
			Version: config.AppVersion,
			Mode:    cfg.App.Mode,
		})
	}
}
