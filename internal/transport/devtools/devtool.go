//go:build debug

// The devtool surface of a debug build: the samber/do web UI under /debug/do,
// the TypeID codecs a developer turns a log line's identifier into the
// UUID a query wants, and the passkey probe pages. The routes sit outside
// the throttled and bearer-guarded groups — they are a developer's window
// into the process, not a client surface — and no authentication guards
// them, because a release build refuses the same paths with a 404 envelope
// (devtool_release.go).

package devtools

import (
	"encoding/json/v2"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"
	dohttp "github.com/samber/do/v2/http"
	"go.jetify.com/typeid"

	"github.com/riipandi/saka/framework/webutil"
	appweb "github.com/riipandi/saka/web"
)

// Mount mounts the debug build's instruments over the router: the samber/do
// container web UI and the TypeID codecs. A release build answers the same
// paths with a 404 envelope rather than letting the SPA claim them.
func Mount(r chi.Router, injector do.Injector) {
	r.Get(devtoolUIPath, func(w http.ResponseWriter, r *http.Request) {
		html, err := dohttp.IndexHTML(devtoolUIPath)
		devtoolPage(w, html, err)
	})
	r.Get(devtoolUIPath+"/scope", func(w http.ResponseWriter, r *http.Request) {
		scopeID := r.URL.Query().Get("scope_id")
		if scopeID == "" {
			http.Redirect(w, r, devtoolUIPath+"/scope?scope_id="+injector.ID(), http.StatusFound)
			return
		}
		html, err := dohttp.ScopeTreeHTML(devtoolUIPath, injector, scopeID)
		devtoolPage(w, html, err)
	})
	r.Get(devtoolUIPath+"/service", func(w http.ResponseWriter, r *http.Request) {
		scopeID := r.URL.Query().Get("scope_id")
		serviceName := r.URL.Query().Get("service_name")
		if scopeID == "" || serviceName == "" {
			html, err := dohttp.ServiceListHTML(devtoolUIPath, injector)
			devtoolPage(w, html, err)
			return
		}
		html, err := dohttp.ServiceHTML(devtoolUIPath, injector, scopeID, serviceName)
		devtoolPage(w, html, err)
	})
	r.Post("/debug/encode-id", encodeID)
	r.Post("/debug/decode-id", decodeID)

	// The passkey probe pages: the simulation of the SPA surfaces the
	// frontend work will replace, one route per flow, carried by the web
	// package's debug build.
	for path, body := range appweb.WebauthnProbePages {
		page := body
		r.Get(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(page))
		})
	}
	for path, body := range appweb.UploadProbePages {
		page := body
		r.Get(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(page))
		})
	}
}

// devtoolID is the codec's answer on both directions: the identifier in its
// TypeID form, plus the pair it decodes from.
type devtoolID struct {
	Prefix string `json:"prefix"`
	UUID   string `json:"uuid"`
	ID     string `json:"id"`
}

// encodeID turns a prefix and a UUID into the TypeID form the API prints.
func encodeID(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prefix string `json:"prefix"`
		UUID   string `json:"uuid"`
	}
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		webutil.BadRequestJSON(w, r, "the body must be JSON with a prefix and a uuid")
		return
	}

	id, err := typeid.FromUUIDWithPrefix(req.Prefix, req.UUID)
	if err != nil {
		webutil.BadRequestJSON(w, r, err.Error())
		return
	}

	webutil.Success(w, r, http.StatusOK, devtoolID{
		Prefix: id.Prefix(), UUID: req.UUID, ID: id.String(),
	}, webutil.WithMessage("the type id was encoded"))
}

// decodeID turns a TypeID string back into the prefix and UUID it carries.
func decodeID(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		webutil.BadRequestJSON(w, r, "the body must be JSON with an id")
		return
	}

	id, err := typeid.FromString(req.ID)
	if err != nil {
		webutil.BadRequestJSON(w, r, err.Error())
		return
	}

	webutil.Success(w, r, http.StatusOK, devtoolID{
		Prefix: id.Prefix(), UUID: id.UUID(), ID: id.String(),
	}, webutil.WithMessage("the type id was decoded"))
}

// devtoolPage renders one of the dohttp pages, whose only failure mode is a
// template error the caller cannot answer with content.
func devtoolPage(w http.ResponseWriter, html string, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}
