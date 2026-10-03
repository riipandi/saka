package fetcher_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/fetcher"
	"github.com/riipandi/saka/internal/registry"
)

func TestRegistryWiresTheClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wired"))
	}))
	t.Cleanup(server.Close)

	cfg := config.Default()
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
