package mailer_test

import (
	"io"
	"testing"

	fwmailer "github.com/riipandi/saka/framework/mailer"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/mailer"
)

func benchView() fwmailer.View {
	return fwmailer.View{Data: mailer.PasswordResetData{
		Email:     "neveu@example.com",
		ResetCode: "expecto-patronum",
	}}
}

func benchTemplates(b *testing.B) *fwmailer.Templates {
	b.Helper()
	templates, err := mailer.NewTemplates(mailer.SenderFrom(config.Default()))
	if err != nil {
		b.Fatal(err)
	}
	return templates
}

// The parsed templates are the cache: Render and RenderTo reuse them, so the
// only work per message is the execution itself. This is the buffered form,
// which builds the whole body as a string before anything can use it.
func BenchmarkRenderPasswordReset(b *testing.B) {
	templates := benchTemplates(b)
	view := benchView()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := templates.Render(mailer.TemplatePasswordReset, view); err != nil {
			b.Fatal(err)
		}
	}
}

// The streaming form writes one rendering into the caller's writer, which is
// what the send path does: the message goes to the SMTP session and never
// becomes a string. The comparison against BenchmarkRenderPasswordReset is the
// number that decides whether the second path earns its keep.
func BenchmarkRenderToPasswordReset(b *testing.B) {
	templates := benchTemplates(b)
	view := benchView()

	b.ReportAllocs()
	for b.Loop() {
		if err := templates.RenderTo(io.Discard, mailer.TemplatePasswordReset, fwmailer.BodyHTML, view); err != nil {
			b.Fatal(err)
		}
	}
}

// Parsing is the cost the cache avoids: it happens once per process, not per
// message, which is the whole reason a parsed template is held.
func BenchmarkParseAllTemplates(b *testing.B) {
	sender := mailer.SenderFrom(config.Default())

	b.ReportAllocs()
	for b.Loop() {
		if _, err := mailer.NewTemplates(sender); err != nil {
			b.Fatal(err)
		}
	}
}
