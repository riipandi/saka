// The shell is the HTML document the Go binary renders for every SPA page:
// the head with its meta and the Vite fragment, and the body with the
// loading state the application replaces. There is no separate index.html
// for Vite to own — the build's input is the application entry, and every
// document the server answers comes from here. A page is one entry point
// plus the meta it carries; a multi-page build registers further pages and
// the router hands each route the page it names, the way the Vite backend
// integration guide's multi-page section describes.
package web

import (
	"bytes"
	"html/template"
)

// Page describes one document the shell renders: the build entry whose
// fragment it embeds, and the head meta it carries.
type Page struct {
	// Entry is the source path the manifest names — "app/main.tsx" for the
	// application shell, another path for a second document.
	Entry string
	// Title is the document title.
	Title string
	// Description is the document's meta description.
	Description string
	// Noindex keeps crawlers away. The application pages carry it; a
	// public page a later phase adds turns it off and gains the meta the
	// preview needs.
	Noindex bool
}

// DefaultPage is the application document every unmatched GET renders.
var DefaultPage = Page{
	Entry:       "app/main.tsx",
	Title:       "Monolith Go React",
	Description: "Monolith Go, React, and TanStack application template",
	Noindex:     true,
}

// RenderPage executes the document for one page with one Vite fragment —
// the page's meta, the tags handed in, and the loader. It is what
// SetupStatic drives per request, and what a caller serving a page under
// its own route uses.
func RenderPage(page Page, tags template.HTML) (template.HTML, error) {
	var buf bytes.Buffer
	tmpl, err := template.New("shell").Parse(shellTmpl)
	if err != nil {
		return "", err
	}
	err = tmpl.Execute(&buf, struct {
		Page
		ViteTags template.HTML
	}{page, tags})
	if err != nil {
		return "", err
	}
	return trustedHead(buf.Bytes()), nil
}

// shellTmpl is the whole document. The loader markup and styles come from
// the index.html this shell replaced: the static state the SPA's mount
// clears, kept as a Go template so the document has one owner.
const shellTmpl = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta name="description" content="{{ .Description }}" />
{{- if .Noindex }}
    <meta name="robots" content="noindex, nofollow, noarchive" />
{{- end }}

    <!-- Theme Color -->
    <meta name="theme-color" media="(prefers-color-scheme: light)" content="#1860FA" />
    <meta name="theme-color" media="(prefers-color-scheme: dark)" content="#000d1a" />

    <!-- Favicon and Manifest -->
    <link rel="manifest" href="/manifest.json" />
    <link rel="shortcut icon" href="/favicon.ico" />
    <link rel="icon" type="image/x-icon" href="/favicon.ico" />
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <link rel="apple-touch-icon" sizes="180x180" href="/favicon.png" />

    <!-- Preload Fonts (pinned major — jsDelivr serves immutable TTLs for ranged versions). -->
    <link rel="preconnect" href="https://cdn.jsdelivr.net" />
    <link
      rel="preload"
      as="font"
      type="font/woff2"
      href="https://cdn.jsdelivr.net/fontsource/fonts/mona-sans:vf@5/latin-wght-normal.woff2"
      crossorigin
    />
    <link
      rel="preload"
      as="font"
      type="font/woff2"
      href="https://cdn.jsdelivr.net/fontsource/fonts/jetbrains-mono:vf@5/latin-wght-normal.woff2"
      crossorigin
    />

    <!-- Page Title -->
    <title>{{ .Title }}</title>

    {{ .ViteTags }}

    <!-- Static SPA loader; React clears #root on mount. -->
    <style>
      html{color-scheme:light dark}
      .s-loader{position:fixed;inset:0;display:grid;place-content:center}
      .s-loader-mark{width:2.25rem;height:auto;fill:oklch(0.55 0.24 262.67)}
      .s-dot{animation:s-pulse 1s ease-in-out infinite}.s-dot-2{animation-delay:150ms}
      .s-dot-3{animation-delay:300ms}
      @keyframes s-pulse{0%,100%{opacity:1}50%{opacity:0.25}}
      @media (prefers-reduced-motion:reduce){.s-dot{animation:none;opacity:0.7}}
    </style>
  </head>
  <body>
    <div id="root" class="isolate">
      <div id="s-loader" class="s-loader" role="status" aria-label="Loading">
        <svg
          class="s-loader-mark"
          aria-hidden="true"
          viewBox="0 0 36 12"
          xmlns="http://www.w3.org/2000/svg"
        >
          <circle class="s-dot" cx="6" cy="6" r="4" />
          <circle class="s-dot s-dot-2" cx="18" cy="6" r="4" />
          <circle class="s-dot s-dot-3" cx="30" cy="6" r="4" />
        </svg>
      </div>
    </div>
  </body>
</html>
`
