//go:build !release

// The SPA surface of a debug build: the engine's dev mount over the app's
// own document and reserved prefixes. The build tags pick the mode; the
// engine carries both.

package web

import (
	"html/template"

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

// devPage is the application document a debug build serves. It appends the
// StyleX dev runtime to the Vite fragment: the compiler's transformIndexHtml
// cannot inject it, because the document is the Go shell's. The pair mirrors
// what transformIndexHtml injects for a Vite-served document: the stylesheet
// link is render-blocking, so the first paint carries the CSS the compiler
// has already collected, and the runtime — served through the module
// pipeline at /@id/, not the raw middleware path — runs with a live HMR
// context, re-fetches the full sheet as the compiler transforms the app's
// modules, and disables the link once its inline copy covers the page.
var devPage = bundler.Page{
	Entry:       "src/main.tsx",
	Title:       "Monolith Go React",
	Description: "Monolith Go, React, and TanStack application template",
	Noindex:     true,
	ExtraHead: template.HTML(
		`<link rel="stylesheet" href="/virtual:stylex.css">` +
			`<script type="module" src="/@id/virtual:stylex:runtime"></script>`),
}

// SetupStatic mounts the SPA surface for a debug build.
func SetupStatic(r chi.Router) {
	bundler.MountDev(r, viteDevServer, devPage, surfacePrefixes...)
}
