package bundler

import (
	"html/template"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTheShellRendersThePageAndTheFragment(t *testing.T) {
	html, err := RenderPage(Page{
		Entry:       "src/main.tsx",
		Title:       "Sign in — Hogwarts",
		Description: "Sign in to your account",
		Noindex:     true,
	}, template.HTML(`<script type="module" src="/assets/app.js"></script>`))
	require.NoError(t, err)

	doc := string(html)
	assert.Contains(t, doc, "<!doctype html>")
	assert.Contains(t, doc, "<title>Sign in — Hogwarts</title>")
	assert.Contains(t, doc, `content="Sign in to your account"`)
	assert.Contains(t, doc, `name="robots" content="noindex`)
	assert.Contains(t, doc, `<script type="module" src="/assets/app.js"></script>`)
	assert.Contains(t, doc, `id="root"`, "the mount point the application replaces")
}

func TestAIndexablePageDropsTheRobotsRefusal(t *testing.T) {
	html, err := RenderPage(Page{Title: "Landing", Description: "d", Noindex: false}, "")
	require.NoError(t, err)
	assert.NotContains(t, string(html), `name="robots"`)
}

func TestTheShellEscapesTheMeta(t *testing.T) {
	html, err := RenderPage(Page{Title: `Evil " onload="x`, Description: "d"}, "")
	require.NoError(t, err)
	assert.Contains(t, string(html), "<title>Evil &#34; onload=&#34;x</title>",
		"html/template escapes the values the page carries")
}
