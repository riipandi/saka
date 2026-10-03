package web

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// manifestFS is a build output carrying two entries and the shared chunk
// they import — the shape a multi-page build produces.
func manifestFS() fstest.MapFS {
	return fstest.MapFS{
		"assets.json": &fstest.MapFile{Data: []byte(`{
			"src/main.tsx": {
				"file": "assets/app-Bq7k.css.e0f.js", "name": "app", "src": "src/main.tsx",
				"isEntry": true, "css": ["assets/app-Bq7k.css"], "imports": ["shared/chunk.tsx"]
			},
			"landing/main.tsx": {
				"file": "assets/landing-X9d2.js", "name": "landing", "src": "landing/main.tsx",
				"isEntry": true
			},
			"shared/chunk.tsx": {
				"file": "assets/chunk-Q1w3.js", "name": "chunk", "src": "shared/chunk.tsx",
				"css": ["assets/chunk-M4n5.css"]
			}
		}`)},
	}
}

func TestTheReleaseFragmentResolvesTheEntry(t *testing.T) {
	f, err := ViteHTMLFragment(ViteConfig{FS: manifestFS(), ViteEntry: "src/main.tsx"})
	require.NoError(t, err)

	tags := string(f.Tags)
	assert.Contains(t, tags, `<script type="module" src="/assets/app-Bq7k.css.e0f.js">`)
	assert.Contains(t, tags, `<link rel="stylesheet" href="/assets/app-Bq7k.css">`)
	assert.Contains(t, tags, `<link rel="modulepreload" href="/assets/chunk-Q1w3.js">`,
		"a statically imported chunk preloads")
	assert.Contains(t, tags, `<link rel="stylesheet" href="/assets/chunk-M4n5.css">`,
		"the shared chunk's styles ride the page that imports it")
	assert.NotContains(t, tags, "landing", "another page's entry never leaks into this fragment")
}

func TestTheMultiPageBuildOwnePerEntryFragments(t *testing.T) {
	app, err := ViteHTMLFragment(ViteConfig{FS: manifestFS(), ViteEntry: "src/main.tsx"})
	require.NoError(t, err)
	landing, err := ViteHTMLFragment(ViteConfig{FS: manifestFS(), ViteEntry: "landing/main.tsx"})
	require.NoError(t, err)

	assert.Contains(t, string(landing.Tags), `src="/assets/landing-X9d2.js"`)
	assert.NotContains(t, string(landing.Tags), "app-Bq7k", "the landing page carries no app asset")
	assert.NotEqual(t, app.Tags, landing.Tags, "two entries resolve to two fragments")
}

func TestTheDevFragmentIsSameOrigin(t *testing.T) {
	f, err := ViteHTMLFragment(ViteConfig{
		IsDev: true, ViteEntry: "src/main.tsx", ViteTemplate: ViteReact,
	})
	require.NoError(t, err)

	tags := string(f.Tags)
	assert.Contains(t, tags, "import RefreshRuntime from '/@react-refresh'",
		"the React preamble rides the dev fragment")
	assert.Contains(t, tags, `<script type="module" src="/@vite/client">`)
	assert.Contains(t, tags, `<script type="module" src="/src/main.tsx">`)
	assert.NotContains(t, tags, "http://", "the browser never learns another port — the Go surface proxies the compiler")
}

func TestTheDevFragmentKeepsAnExplicitBase(t *testing.T) {
	f, err := ViteHTMLFragment(ViteConfig{
		IsDev: true, ViteURL: "http://localhost:5173",
		ViteEntry: "src/main.tsx", ViteTemplate: ViteReact,
	})
	require.NoError(t, err)

	tags := string(f.Tags)
	assert.Contains(t, tags, "import RefreshRuntime from 'http://localhost:5173/@react-refresh'")
	assert.Contains(t, tags, `<script type="module" src="http://localhost:5173/src/main.tsx">`)
}

func TestTheVanillaDevFragmentSkipsThePreamble(t *testing.T) {
	f, err := ViteHTMLFragment(ViteConfig{
		IsDev: true, ViteEntry: "src/main.tsx", ViteTemplate: ViteNone,
	})
	require.NoError(t, err)
	assert.NotContains(t, string(f.Tags), "RefreshRuntime")
}

func TestAnUnknownEntryIsAnErrorNotASilentTag(t *testing.T) {
	_, err := ViteHTMLFragment(ViteConfig{FS: manifestFS(), ViteEntry: "missing/main.tsx"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing/main.tsx")
}

func TestAManifestLessConfigIsRefusedOutsideDev(t *testing.T) {
	_, err := ViteHTMLFragment(ViteConfig{ViteEntry: "src/main.tsx"})
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "vite:"), "the error names its package")
}
