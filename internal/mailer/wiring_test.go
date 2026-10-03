package mailer_test

import (
	"log/slog"
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/mailer"
	"github.com/riipandi/saka/internal/registry"
)

func TestRegistryWiresTheService(t *testing.T) {
	// The composition root is the one place the service is built, so this is
	// what proves serve gets the same value a feature does. The default
	// configuration names no SMTP host, which is the state a fresh checkout is
	// in: the service must still build, with templates ready and a mailer that
	// refuses to send.
	cfg := config.Default()
	injector := registry.New(t.Context(), cfg, nil, slog.New(slog.DiscardHandler))
	t.Cleanup(func() {
		report := injector.Shutdown()
		if report != nil {
			assert.True(t, report.Succeed)
		}
	})

	service, err := do.Invoke[*mailer.Service](injector)
	require.NoError(t, err)
	require.NotNil(t, service)

	assert.False(t, service.Configured())
	assert.ErrorIs(t,
		service.Send(t.Context(), mailer.Request{Template: mailer.TemplateTestEmail}),
		mailer.ErrNotConfigured)

	// The templates are the embedded set, not an empty placeholder.
	body, err := service.Templates().Render(mailer.TemplateTestEmail, mailer.View{
		Email: "neveu@example.com",
		Data:  mailer.TestEmailData{Email: "neveu@example.com"},
	})
	require.NoError(t, err)
	assert.Contains(t, body.HTML, "neveu@example.com")
}
