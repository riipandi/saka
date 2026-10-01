package web

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// ViteManifest is the build manifest vite build writes, as described in the
// [Vite manifest]. Backend integration reads it to resolve source entries
// to the hashed artifacts of the build.
//
// [Vite manifest]: https://vite.dev/guide/api-plugin.html#manifest
type ViteManifest map[string]*ViteChunk

// A ViteChunk is one entry of the manifest.
type ViteChunk struct {
	File           string   `json:"file"`
	Name           string   `json:"name"`
	Src            string   `json:"src"`
	CSS            []string `json:"css"`
	IsDynamicEntry bool     `json:"isDynamicEntry"`
	IsEntry        bool     `json:"isEntry"`
	Imports        []string `json:"imports"`
	DynamicImports []string `json:"dynamicImports"`
}

// ParseViteManifest parses the manifest file.
func ParseViteManifest(r io.Reader) (*ViteManifest, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var m ViteManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// entryPoint returns the manifest's single entry point, or nil.
func (m ViteManifest) entryPoint() *ViteChunk {
	for _, chunk := range m {
		if chunk.IsEntry {
			return chunk
		}
	}
	return nil
}

// chunkFor returns the chunk the given source path maps to, or nil.
func (m ViteManifest) chunkFor(name string) *ViteChunk {
	return m[name]
}

// viteReactPreamble returns the script tag that enables React Fast
// Refresh against the dev server.
func viteReactPreamble(server string) string {
	refresh, _ := url.JoinPath(server, "/@react-refresh")
	return fmt.Sprintf(`<script type="module">
  import RefreshRuntime from '%s'
  RefreshRuntime.injectIntoGlobalHook(window)
  window.$RefreshReg$ = () => {}
  window.$RefreshSig$ = () => (type) => type
  window.__vite_plugin_react_preamble_installed__ = true
</script>`, refresh)
}

// generateCSS renders the stylesheet links for the chunk, following the
// chunk's imports so a shared chunk's styles ride the page that needs it.
func (m ViteManifest) generateCSS(name, prefix string) string {
	var sb strings.Builder
	seen := make(map[string]bool)

	var addCSS func(string)
	addCSS = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true

		chunk, ok := m[name]
		if !ok {
			return
		}

		for _, css := range chunk.CSS {
			sb.WriteString(`<link rel="stylesheet" href="`)
			sb.WriteString(prefix)
			sb.WriteString("/")
			sb.WriteString(css)
			sb.WriteString(`">`)
		}

		for _, imp := range chunk.Imports {
			addCSS(imp)
		}
	}

	addCSS(name)

	return sb.String()
}

// generateModules renders the module script for the chunk.
func (m ViteManifest) generateModules(name, prefix string) string {
	chunk, ok := m[name]
	if !ok {
		return ""
	}

	var sb strings.Builder
	if chunk.File != "" {
		sb.WriteString(`<script type="module" src="`)
		sb.WriteString(prefix)
		sb.WriteString("/")
		sb.WriteString(chunk.File)
		sb.WriteString(`"></script>`)
	}

	return sb.String()
}

// generatePreloadModules renders the modulepreload links for the chunk and
// the chunks it statically imports.
func (m ViteManifest) generatePreloadModules(name, prefix string) string {
	var sb strings.Builder
	seen := make(map[string]bool)

	var addModulePreload func(string)
	addModulePreload = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true

		chunk, ok := m[name]
		if !ok {
			return
		}

		if chunk.File != "" {
			sb.WriteString(`<link rel="modulepreload" href="`)
			sb.WriteString(prefix)
			sb.WriteString("/")
			sb.WriteString(chunk.File)
			sb.WriteString(`">`)
		}

		for _, imp := range chunk.Imports {
			addModulePreload(imp)
		}
	}

	addModulePreload(name)

	return sb.String()
}
