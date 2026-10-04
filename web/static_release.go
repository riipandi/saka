//go:build release

// The SPA surface of a release build: the engine's release mount over the
// app's embedded output and its own document. The build tags pick the
// mode; the engine carries both.

package web

import (
	"github.com/go-chi/chi/v5"
	"github.com/riipandi/saka/framework/bundler"
)

// SetupStatic mounts the SPA surface: the built assets from the embedded
// output, and the Go-rendered shell for every path the routes above left
// unclaimed.
func SetupStatic(r chi.Router) {
	bundler.MountRelease(r, OutputFS(), DefaultPage, surfacePrefixes...)
}
