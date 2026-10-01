package web

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
)

// ViteFragment holds the head tags the shell embeds for one entry point.
type ViteFragment struct {
	// Tags is the rendered script and link tags. It is template.HTML so the
	// shell's template renders it without escaping.
	Tags template.HTML
}

// ViteHTMLFragment resolves the head tags for one entry point out of the
// configuration. In development it names the dev server's client and the
// entry module by same-origin paths — the browser never learns another
// port, because the Go surface in front proxies the dev server. In
// production it resolves the entry through the build manifest to its
// hashed module, stylesheets, and preload links.
func ViteHTMLFragment(config ViteConfig) (*ViteFragment, error) {
	pd := &vitePageData{}

	if config.IsDev {
		base := strings.TrimSuffix(config.ViteURL, "/")
		pd.ClientURL = base + "/@vite/client"
		pd.EntryURL = base + "/" + strings.TrimPrefix(config.ViteEntry, "/")
		if config.ViteTemplate.RequiresPreamble() {
			pd.ReactPreamble = trustedHead(viteReactPreamble(base))
		}
	} else {
		if config.FS == nil {
			return nil, fmt.Errorf("vite: the build output FS is required outside development")
		}
		if config.ViteManifest == "" {
			config.ViteManifest = ".vite/manifest.json"
		}
		mf, err := config.FS.Open(config.ViteManifest)
		if err != nil {
			return nil, fmt.Errorf("vite: open manifest: %w", err)
		}
		defer mf.Close()

		m, err := ParseViteManifest(mf)
		if err != nil {
			return nil, fmt.Errorf("vite: parse manifest: %w", err)
		}
		var chunk *ViteChunk
		if config.ViteEntry == "" {
			chunk = m.entryPoint()
		} else {
			chunk = m.chunkFor(config.ViteEntry)
		}
		if chunk == nil {
			return nil, fmt.Errorf("vite: unable to find chunk for entry point %q", config.ViteEntry)
		}

		pd.StyleSheets = trustedHead(m.generateCSS(chunk.Src, config.AssetsURLPrefix))
		pd.Modules = trustedHead(m.generateModules(chunk.Src, config.AssetsURLPrefix))
		pd.PreloadModules = trustedHead(m.generatePreloadModules(chunk.Src, config.AssetsURLPrefix))
	}

	var buf bytes.Buffer
	tmpl, err := template.New("vite").Parse(viteHeadTmpl)
	if err != nil {
		return nil, fmt.Errorf("vite: parse template: %w", err)
	}
	if err := tmpl.Execute(&buf, pd); err != nil {
		return nil, fmt.Errorf("vite: execute template: %w", err)
	}
	return &ViteFragment{Tags: trustedHead(buf.Bytes())}, nil
}

// trustedHead marks fragment bytes as HTML for the shell's template. The
// fragment is assembled from the build manifest and the documented dev
// server paths — neither carries request input — and it is the only place
// the shell receives unescaped markup; every value a page names goes
// through html/template's escaping.
func trustedHead[T []byte | string](raw T) template.HTML {
	return template.HTML(raw)
}

// vitePageData feeds the fragment template. Its fields are exported
// because html/template refuses unexported ones.
type vitePageData struct {
	ClientURL      string
	EntryURL       string
	ReactPreamble  template.HTML
	StyleSheets    template.HTML
	Modules        template.HTML
	PreloadModules template.HTML
}

// viteHeadTmpl renders the head tags. In development the paths are
// same-origin; the Go surface proxies whatever they name. In production
// the manifest has already resolved every path.
const viteHeadTmpl = `
{{- if .ClientURL }}
	{{- if .ReactPreamble }}
	{{ .ReactPreamble }}
	{{- end }}
	<script type="module" src="{{ .ClientURL }}"></script>
	<script type="module" src="{{ .EntryURL }}"></script>
{{- end }}
{{- if .StyleSheets }}
	{{ .StyleSheets }}
{{- end }}
{{- if .Modules }}
	{{ .Modules }}
{{- end }}
{{- if .PreloadModules }}
	{{ .PreloadModules }}
{{- end }}
`
