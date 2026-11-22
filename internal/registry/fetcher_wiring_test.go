package registry_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/fetcher"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/registry"
)

func TestRegistryWiresTheClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wired"))
	}))
	t.Cleanup(server.Close)

	cfg := config.Default()
	// The server is on loopback, which the default policy refuses.
	cfg.Fetcher.AllowPrivateNetwork = true
	injector := registry.New(t.Context(), cfg, nil, slog.New(slog.DiscardHandler))
	t.Cleanup(func() {
		report := injector.Shutdown()
		if report != nil {
			assert.True(t, report.Succeed)
		}
	})

	client, err := do.Invoke[*fetcher.Client](injector)
	require.NoError(t, err)
	res, err := client.Do(t.Context(), fetcher.Request{URL: server.URL + "/status"})
	require.NoError(t, err)
	assert.Equal(t, "wired", string(res.Body))

	require.NoError(t, client.Shutdown(t.Context()))
}

// TestTheDefaultConfigurationRefusesThePrivateNetwork pins the wiring of the
// SSRF stance: the shared client the registry resolves refuses a loopback
// destination unless fetcher.allow_private_network says otherwise.
func TestTheDefaultConfigurationRefusesThePrivateNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("reached"))
	}))
	t.Cleanup(server.Close)

	injector := registry.New(t.Context(), config.Default(), nil, slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = injector.Shutdown() })

	client, err := do.Invoke[*fetcher.Client](injector)
	require.NoError(t, err)
	_, err = client.Do(t.Context(), fetcher.Request{URL: server.URL})
	require.ErrorIs(t, err, fetcher.ErrDestinationRefused)
}
