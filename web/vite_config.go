// Vite integrates a Vite-built frontend with the Go binary, following the
// Vite backend integration guide. It is a port of olivere/vite (MIT)
// reduced to what this repository serves: the fragment of head tags the
// shell embeds, resolved from the build manifest in release and from the
// dev server in debug. Upstream is not tracked; the fragment engine is
// owned here. Serving the assets themselves stays with the SetupStatic
// seam, and the HTML document stays with the shell.
//
// One entry point owns one fragment: a multi-page build passes a different
// ViteEntry per document and gets that page's module, its stylesheets, and
// its preload links — never another page's.
package web

import "io/fs"

// Config is the configuration for one fragment.
type ViteConfig struct {
	// FS is the Vite build output. It is read only in production mode, for
	// the manifest that maps source entries to hashed artifacts.
	FS fs.FS

	// IsDev links the fragment to the Vite dev server instead of the built
	// assets. The debug binary is in dev mode; the release binary is not.
	IsDev bool

	// ViteEntry is the source path of the entry point, as the manifest
	// names it — "app/main.tsx" for the application shell. A multi-page
	// build carries one entry per document; an empty entry asks the
	// manifest for its single entry point and fails when there are many.
	ViteEntry string

	// ViteURL is the dev server origin the fragment's same-origin paths
	// ride. It is unused in production mode.
	ViteURL string

	// ViteManifest is the manifest path relative to FS. It defaults to
	// "assets.json", the derived copy the build writes for the embed.
	ViteManifest string

	// ViteTemplate names the frontend scaffolding whose dev preamble the
	// fragment injects. It is unused in production.
	ViteTemplate ViteScaffolding

	// AssetsURLPrefix is the URL prefix the built asset paths are served
	// under, for a deployment that keeps the artifacts away from the
	// document origin. It defaults to "" — the assets ride the binary.
	AssetsURLPrefix string
}

// ViteScaffolding names the frontend stack whose dev-mode preamble the
// fragment injects. Only the stacks this project builds with are carried.
type ViteScaffolding int

const (
	// ViteNone injects no preamble.
	ViteNone ViteScaffolding = iota

	// ViteReact injects the React Fast Refresh preamble.
	ViteReact
)

// RequiresPreamble answers whether the scaffolding needs one.
func (s ViteScaffolding) RequiresPreamble() bool {
	return s == ViteReact
}

// Preamble returns the preamble script for the scaffolding, pointing at
// the given dev server.
func (s ViteScaffolding) Preamble(viteURL string) string {
	if s != ViteReact {
		return ""
	}
	return viteReactPreamble(viteURL)
}
