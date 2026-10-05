package web

import "embed"

//go:embed email/*.tmpl
var EmailTemplates embed.FS
