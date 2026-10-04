//go:build !release

// The SPA surface of a debug build: the engine's dev mount over the app's
// own document and reserved prefixes. The build tags pick the mode; the
// engine carries both.

package web

import (
	"github.com/go-chi/chi/v5"
	"github.com/riipandi/saka/framework/bundler"
)

// viteDevServer is where `task dev` runs the Vite dev server — the
// compiler this build borrows. It is a development-only constant, not
// configuration: a deployment never serves a debug binary, and the
// release build resolves every tag from its embedded manifest. The server
// is an implementation detail behind the Go port, never an origin the
// developer opens; the fragment's tags are same-origin, and the proxy
// below is what answers them.
const viteDevServer = "http://127.0.0.1:5173"

// SetupStatic mounts the SPA surface for a debug build.
func SetupStatic(r chi.Router) {
	bundler.MountDev(r, viteDevServer, DefaultPage, surfacePrefixes...)
}
