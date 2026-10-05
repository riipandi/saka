package testutils

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
)

// Valkey is a running key-value backend container.
type Valkey struct {
	// URL is the connection string a client parses
	// (redis://host:port/0).
	URL string

	// Host and Port name the server separately, for a report that renders
	// the target without credentials.
	Host string
	Port string
}

// valkeyImage is the key-value backend used by integration tests. It is the
// same image compose starts, so a test exercises what a local run does.
const valkeyImage = "docker.io/valkey/valkey:9.2-alpine"

var (
	valkeyOnce   sync.Once
	sharedValkey *Valkey
	valkeyErr    error
)

// StartValkey returns the shared key-value backend container for the test
// binary. A test that needs its own state looks for its own key prefix
// rather than starting a second container: the backend is shared, the
// namespaces are not.
func StartValkey(ctx context.Context, t testing.TB) *Valkey {
	t.Helper()

	SkipWithoutDocker(t)

	valkeyOnce.Do(func() {
		container, startErr := tcvalkey.Run(ctx, valkeyImage)
		if startErr != nil {
			valkeyErr = startErr
			return
		}

		sharedValkey = &Valkey{}
		if sharedValkey.URL, startErr = container.ConnectionString(ctx); startErr != nil {
			valkeyErr = startErr
			return
		}
		if sharedValkey.Host, startErr = container.Host(ctx); startErr != nil {
			valkeyErr = startErr
			return
		}
		port, portErr := container.MappedPort(ctx, "6379/tcp")
		if portErr != nil {
			valkeyErr = portErr
			return
		}
		sharedValkey.Port = port.Port()
	})

	require.NoError(t, valkeyErr, "start valkey container (docker daemon required)")
	return sharedValkey
}

// StartValkeyWithTimeout starts the backend and bounds the wait, so a test
// that cannot get a container fails quickly instead of hanging the binary.
func StartValkeyWithTimeout(t testing.TB) *Valkey {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return StartValkey(ctx, t)
}
