package web

import (
	"embed"
	"io/fs"

	"github.com/riipandi/saka/framework/bundler"
)

// The app binding of the bundler: the document every SPA page renders as,
// and the embedded build output the release mount serves. The engine lives
// in framework/bundler; this package carries what is Saka's own.

//go:embed all:output
var webFS embed.FS

// OutputFS answers the embedded build output — the derived manifest the
// fragment resolves against, and the hashed assets the surface serves.
// The release build is the only one that carries it. The `all:` prefix is
// load-bearing: the plain pattern excludes files whose names begin with an
// underscore or a dot, and the router's code-split chunks are named
// `_route-…` — a release build without it answers every route chunk with
// the document and no code-split page ever loads.
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
