package mailer

import (
	"strings"

	fwmailer "github.com/riipandi/saka/framework/mailer"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/web"
)

// The app binding is the seam between the framework's mailer engine and this
// application: the embedded template set the Vite build compiles, the sender
// identity the configuration resolves, and the template catalog beside it
// (data.go).

// NewTemplates parses the templates embedded in this binary.
func NewTemplates(sender fwmailer.Sender) (*fwmailer.Templates, error) {
	return fwmailer.NewTemplatesFS(web.EmailTemplates, sender)
}

// SenderFrom resolves the identity from the configuration.
//
// The logo is an absolute URL, because a mail client fetches it from outside
// this process. The public origin is `app.base_url`: the application serves the
// bundle the image ships in, so its own origin is always right. `app.assets_url`
// is the fallback for a deployment that publishes its assets on another origin
// and names no public base URL.
func SenderFrom(cfg config.Config) fwmailer.Sender {
	origin := cfg.App.BaseURL
	if origin == "" {
		origin = cfg.App.AssetsURL
	}
	return fwmailer.Sender{
		AppName: config.AppName,
		LogoURL: strings.TrimSuffix(origin, "/") + "/images/logoEmail.svg",
	}
}
