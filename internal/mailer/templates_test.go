package mailer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/mailer"
)

// sender is the identity every template renders with.
func sender() mailer.Sender {
	return mailer.SenderFrom(config.Default())
}

func TestNewTemplatesParsesTheEmbeddedSet(t *testing.T) {
	// The compiled templates are embedded in the binary, so this fails if the
	// Vite build was skipped or a template does not parse — a broken build, not
	// a bad configuration.
	templates, err := mailer.NewTemplates(sender())
	require.NoError(t, err)

	// Every template the email/ directory defines, so a template added there
	// without a Go-side fixture is noticed here.
	assert.Equal(t, []string{
		mailer.TemplateAPIKeyExpiringSoon,
		mailer.TemplateEmailChangeNotice,
		mailer.TemplateEmailChangeRequest,
		mailer.TemplateEmailChangeSuccess,
		mailer.TemplateEmailVerification,
		mailer.TemplateLoginNewDevice,
		mailer.TemplateOneTimeAccess,
		mailer.TemplatePasswordChangedNotice,
		mailer.TemplatePasswordReset,
		mailer.TemplateTestEmail,
		mailer.TemplateUserBanned,
		mailer.TemplateUserUnbanned,
	}, templates.Names())
}

// fixtures is one value per template, holding every field that template names.
// The render fails on a field the value does not have, so a renamed field in a
// template is caught here rather than by an empty string in a sent message.
func fixtures() map[string]mailer.View {
	return map[string]mailer.View{
		mailer.TemplateAPIKeyExpiringSoon: {Data: mailer.APIKeyExpiringSoonData{
			Name: "Andi", APIKeyName: "deploy key", ExpiresAt: "2 January 2026",
		}},
		mailer.TemplateEmailChangeNotice: {Data: mailer.EmailChangeNoticeData{
			Name: "Andi", OldEmail: "flamel@example.com", NewEmail: "neville@example.com",
		}},
		mailer.TemplateEmailChangeRequest: {Data: mailer.EmailChangeRequestData{
			Name: "Andi", OldEmail: "flamel@example.com", NewEmail: "neville@example.com",
			ConfirmLink: "https://app.example.com/confirm?token=abc",
		}},
		mailer.TemplateEmailChangeSuccess: {Data: mailer.EmailChangeSuccessData{
			Name: "Andi", NewEmail: "neville@example.com",
		}},
		mailer.TemplateEmailVerification: {Data: mailer.EmailVerificationData{
			UserFullName: "Andi Pratama", VerificationLink: "https://app.example.com/verify?code=abc",
		}},
		mailer.TemplateLoginNewDevice: {Data: mailer.LoginNewDeviceData{
			City: "Jakarta", Country: "Indonesia", IPAddress: "203.0.113.7",
			Device: "Chrome on macOS", DateTime: time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC),
		}},
		mailer.TemplateOneTimeAccess: {Data: mailer.OneTimeAccessData{
			Code: "123456", LoginLink: "https://app.example.com/signin?method=code",
			LoginLinkWithCode: "https://app.example.com/signin?code=123456",
			ExpirationString:  "5 minutes",
		}},
		mailer.TemplatePasswordReset: {Data: mailer.PasswordResetData{
			Email: "neveu@example.com", ResetLink: "https://app.example.com/reset?token=abc",
		}},
		mailer.TemplatePasswordChangedNotice: {Data: mailer.PasswordChangedNoticeData{
			Name: "Neveu", Email: "neveu@example.com",
		}},
		mailer.TemplateTestEmail: {Email: "neveu@example.com", Data: mailer.TestEmailData{
			Email: "neveu@example.com",
		}},
	}
}

func TestEveryTemplateRendersBothBodies(t *testing.T) {
	templates, err := mailer.NewTemplates(sender())
	require.NoError(t, err)

	for name, view := range fixtures() {
		body, err := templates.Render(name, view)
		require.NoError(t, err, name)
		assert.NotEmpty(t, body.HTML, name)
		assert.NotEmpty(t, body.Text, name)
		// The Go template actions are compiled away, so a body that still
		// contains one is a rendering that did not happen.
		assert.NotContains(t, body.HTML, "{{", name)
		assert.NotContains(t, body.Text, "{{", name)
	}
}

func TestRenderedBodiesCarryTheIdentityAndTheData(t *testing.T) {
	templates, err := mailer.NewTemplates(mailer.Sender{AppName: "Tango", LogoURL: "https://cdn.example.com/logo.svg"})
	require.NoError(t, err)

	body, err := templates.Render(mailer.TemplatePasswordReset, fixtures()[mailer.TemplatePasswordReset])
	require.NoError(t, err)

	assert.Contains(t, body.HTML, "https://cdn.example.com/logo.svg")
	assert.Contains(t, body.HTML, "Tango")
	assert.Contains(t, body.HTML, "https://app.example.com/reset?token=abc")
	assert.Contains(t, body.Text, "Tango")
	assert.Contains(t, body.Text, "https://app.example.com/reset?token=abc")
}

func TestHTMLBodyEscapesUntrustedValues(t *testing.T) {
	// The recipient's address and every template field come from user input, so
	// the HTML rendering escapes them. A name carrying markup must not become
	// markup in the message.
	templates, err := mailer.NewTemplates(sender())
	require.NoError(t, err)

	body, err := templates.Render(mailer.TemplateEmailVerification, mailer.View{
		Data: mailer.EmailVerificationData{
			UserFullName:     `<script>alert(1)</script>`,
			VerificationLink: "https://app.example.com/verify?code=abc",
		},
	})
	require.NoError(t, err)

	assert.NotContains(t, body.HTML, "<script>")
	assert.Contains(t, body.HTML, "&lt;script&gt;")
	// The plain-text body is not HTML, so it keeps the characters as written
	// rather than showing the recipient an entity.
	assert.Contains(t, body.Text, "<script>")
}

func TestUnknownTemplateIsRefused(t *testing.T) {
	templates, err := mailer.NewTemplates(sender())
	require.NoError(t, err)

	_, err = templates.Render("no-such-template", mailer.View{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-template")
	// The message names what is available, so the typo is fixed without reading
	// the source.
	assert.Contains(t, err.Error(), mailer.TemplatePasswordReset)
}

func TestSenderLogoFollowsThePublicOrigin(t *testing.T) {
	// The image is fetched by a mail client, so the URL has to be one a reader
	// outside this process can resolve.
	cfg := config.Default()
	cfg.App.BaseURL = "https://app.example.com/"

	got := mailer.SenderFrom(cfg)
	assert.Equal(t, "https://app.example.com/images/logoEmail.svg", got.LogoURL,
		"a trailing slash in the origin must not double up")
	assert.Equal(t, config.AppName, got.AppName)
}

func TestSenderLogoFallsBackToTheAssetURL(t *testing.T) {
	// A deployment that publishes its assets on another origin and names no
	// public base URL still gets a resolvable logo.
	cfg := config.Default()
	cfg.App.BaseURL = ""
	cfg.App.AssetsURL = "https://cdn.example.com/assets"

	assert.Equal(t, "https://cdn.example.com/assets/images/logoEmail.svg", mailer.SenderFrom(cfg).LogoURL)
}

func TestRenderDoesNotLeakTheTemplateSource(t *testing.T) {
	// A body is what the recipient reads; the template's own define block is
	// not part of it.
	templates, err := mailer.NewTemplates(sender())
	require.NoError(t, err)

	body, err := templates.Render(mailer.TemplateTestEmail, fixtures()[mailer.TemplateTestEmail])
	require.NoError(t, err)

	assert.False(t, strings.HasPrefix(body.HTML, `{{define`))
	assert.NotContains(t, body.HTML, "TemplateProps")
}
