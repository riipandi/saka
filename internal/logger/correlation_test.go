package logger_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/logger"
)

// exportedRecords decodes the first export the collector received into its log
// records. The body was stored decompressed, so it unmarshals directly.
func exportedRecords(t *testing.T, body []byte) []*logspb.LogRecord {
	t.Helper()

	req := &collectorlogs.ExportLogsServiceRequest{}
	require.NoError(t, proto.Unmarshal(body, req))

	resourceLogs := req.GetResourceLogs()
	require.NotEmpty(t, resourceLogs)
	scopeLogs := resourceLogs[0].GetScopeLogs()
	require.NotEmpty(t, scopeLogs)
	return scopeLogs[0].GetLogRecords()
}

// recordBody returns the message text of one log record.
func recordBody(record *logspb.LogRecord) string {
	return record.GetBody().GetStringValue()
}

func TestSlogContextCorrelatesLogsWithTheActiveSpan(t *testing.T) {
	collector := newOTLPTestServer(t)

	cfg := config.Default()
	cfg.Log.Transport = []string{config.LogTransportOTLP}
	cfg.OTEL.Endpoint = collector.URL

	log, err := logger.New(cfg, logger.WithWriter(&bytes.Buffer{}))
	require.NoError(t, err)

	traceID, err := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("0123456789abcdef")
	require.NoError(t, err)
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(t.Context(), spanCtx)

	// The *Context call carries the span, the plain call does not: that is the
	// whole difference the call site decides.
	log.Slog().InfoContext(ctx, "correlated line")
	log.Slog().Info("uncorrelated line")
	require.NoError(t, log.Shutdown(t.Context()))

	requests := collector.requests()
	require.NotEmpty(t, requests, "a record must reach the collector")

	records := exportedRecords(t, requests[0].body)
	require.Len(t, records, 2, "both lines must arrive in one export")

	correlated, uncorrelated := records[0], records[1]
	assert.Equal(t, "correlated line", recordBody(correlated))
	assert.Equal(t, "uncorrelated line", recordBody(uncorrelated))

	assert.Equal(t, traceID[:], correlated.GetTraceId(), "the *Context call carries the trace")
	assert.Equal(t, spanID[:], correlated.GetSpanId(), "the *Context call carries the span")
	assert.Empty(t, uncorrelated.GetTraceId(), "a plain Info has no trace to correlate")
}
