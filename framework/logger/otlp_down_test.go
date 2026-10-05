package logger_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/logger"
)

func TestACollectorThatIsDownDoesNotStopConstruction(t *testing.T) {
	// An exporter does not dial when it is built, and it must not: a service
	// whose collector is briefly unreachable has to start, log locally, and
	// recover when the collector comes back. A constructor that dialled would
	// turn an observability outage into an application outage.
	opts := testOptions()
	opts.Transports = []logger.Transport{logger.TransportConsole, logger.TransportOTLP}
	opts.OTLP.Endpoint = "http://127.0.0.1:1"

	buf := &bytes.Buffer{}
	log, err := logger.New(opts, logger.WithWriter(buf))
	require.NoError(t, err)

	log.Slog().Info("the collector is down")
	log.Flush()

	assert.Contains(t, buf.String(), "the collector is down", "the local sink still received the entry")

	// The drain at shutdown is the first time the exporter dials, so this is
	// where the failure surfaces. It is reported, not swallowed: a deployment
	// that lost its logs should learn it here rather than from an empty
	// dashboard.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	assert.Error(t, log.Shutdown(ctx))
}
