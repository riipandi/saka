package web

import (
	"embed"
	"io/fs"

	"github.com/riipandi/saka/framework/bundler"
)

// The app binding of the bundler: the document every SPA page renders as,
// and the embedded build output the release mount serves. The engine lives
// in framework/bundler; this package carries what is Saka's own.

//go:embed output
var webFS embed.FS

// OutputFS answers the embedded build output — the derived manifest the
// fragment resolves against, and the hashed assets the surface serves.
// The release build is the only one that carries it. Vite's own
// `.vite/manifest.json` stays out: a dot directory is below the embed
// pattern's floor.
func OutputFS() fs.FS {
	artifact, _ := fs.Sub(webFS, "output")
	return artifact
}

// DefaultPage is the application document every unmatched GET renders.
var DefaultPage = bundler.Page{
	Entry:       "src/main.tsx",
	Title:       "Monolith Go React",
	Description: "Monolith Go, React, and TanStack application template",
	Noindex:     true,
}

// surfacePrefixes lists the protocol and API prefixes the JSON envelope
// answers; the shell and the assets never claim them.
var surfacePrefixes = []string{"/.well-known", "/api", "/rpc", "/metrics", "/storage", "/oauth", "/oidc", "/debug"}
