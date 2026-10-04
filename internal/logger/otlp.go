package logger

import (
	"context"
	"fmt"
	"net/url"

	"go.loglayer.dev/transports/otellog/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/observer"
)

// otlpPath is the path the OTLP protocol serves logs on. An endpoint that names
// no path gets it, so the common case of a bare host:port works without the
// caller knowing the protocol.
const otlpPath = "/v1/logs"

// otlpSink is the collector transport and the provider behind it, held together
// because the provider owns the queue that Shutdown has to drain.
type otlpSink struct {
	transport *otellog.Transport
	provider  *sdklog.LoggerProvider
}

// newOTLPSink builds the OpenTelemetry sink the configuration names.
//
// Every exporter setting is passed explicitly, including the ones left at their
// default. The exporter otherwise reads OTEL_EXPORTER_OTLP_* variables for its
// endpoint, headers, compression, and TLS material, which would be a second
// configuration source deciding what a deployment logs and where: a stray export
// in a shell could redirect the logs, inject a header, or trust a certificate
// the config file never mentioned. Passing a value for each closes that door and
// leaves the config file as the only thing that decides.
//
// The address is otel.endpoint, shared with traces and metrics: one collector
// receives every signal, so a second copy of the address could disagree with it.
func newOTLPSink(cfg config.Config) (*otlpSink, error) {
	exporter, err := newLogExporter(cfg)
	if err != nil {
		return nil, err
	}

	// The resource is built rather than taken from resource.Default, which reads
	// OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES: the service a record is
	// attributed to is a property of this program, not of the shell that started
	// it. The identifier rather than the display name, because a service name is
	// matched by tooling, not read by a person.
	//
	// The same attributes the observer puts on a span or a measurement, so one
	// collector groups the three signals of this service together.
	attributes := []attribute.KeyValue{
		semconv.ServiceName(cfg.OTEL.ServiceName),
		semconv.ServiceVersion(config.AppVersion),
	}
	if cfg.OTEL.Environment != "" {
		attributes = append(attributes, semconv.DeploymentEnvironmentNameKey.String(cfg.OTEL.Environment))
	}

	// The batch processor is what keeps logging off the request path: a record
	// is enqueued and the caller returns, while a background goroutine drains
	// the queue. A full queue drops rather than blocking, so a collector that is
	// down costs dropped records and never a slow request.
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter,
			sdklog.WithMaxQueueSize(cfg.OTEL.QueueMaxSize),
		)),
		sdklog.WithResource(resource.NewSchemaless(attributes...)),
	)

	return &otlpSink{
		transport: otellog.New(otellog.Config{
			// The instrumentation scope names the emitting service, so a
			// collector can attribute a record without reading its body.
			Name:           config.AppIdentifier,
			Version:        config.AppVersion,
			LoggerProvider: provider,
			ID:             "otlp",
		}),
		provider: provider,
	}, nil
}

// newLogExporter builds the log exporter.
//
// Every exporter setting is passed explicitly, including the ones left at their
// default. The exporter otherwise reads OTEL_EXPORTER_OTLP_* variables for its
// endpoint, headers, compression, and TLS material, which would be a second
// configuration source deciding what a deployment logs and where: a stray export
// in a shell could redirect the logs, inject a header, or trust a certificate
// the config file never mentioned. Passing a value for each closes that door and
// leaves the config file as the only thing that decides.
func newLogExporter(cfg config.Config) (sdklog.Exporter, error) {
	endpoint, err := url.Parse(cfg.OTEL.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("logger: otlp endpoint: %w", err)
	}

	options := []otlploghttp.Option{
		otlploghttp.WithEndpointURL(cfg.OTEL.Endpoint),
		otlploghttp.WithHeaders(cfg.OTEL.Headers),
		otlploghttp.WithCompression(logCompression(cfg)),
		otlploghttp.WithTimeout(cfg.Log.OTLP.Timeout),
	}
	// A route is applied only when the configuration names one, or when the
	// endpoint carries no path of its own: an address that already names a route
	// — a collector mounted under a prefix, or a backend whose route is not the
	// protocol's — says where the logs go, and overriding it with the default
	// would send them somewhere the user did not ask for. An explicit
	// log.otlp.path wins over both, being the one thing that is unambiguous.
	if path := observer.SignalPath(cfg.Log.OTLP.Path, endpoint.Path, otlpPath); path != "" {
		options = append(options, otlploghttp.WithURLPath(path))
	}
	// A nil TLS configuration is not the same as leaving the option out: it is
	// what stops the exporter from loading OTEL_EXPORTER_OTLP_CERTIFICATE and
	// friends. A secure endpoint gets the floor of TLS 1.2 and the system's root
	// certificates, because the config file names no certificate of its own.
	options = append(options, otlploghttp.WithTLSClientConfig(observer.TLSConfig(cfg.CollectorSecure())))

	exporter, err := otlploghttp.New(context.Background(), options...)
	if err != nil {
		return nil, fmt.Errorf("logger: otlp exporter: %w", err)
	}
	return exporter, nil
}

// logCompression maps the configured name to the exporter's own value.
func logCompression(cfg config.Config) otlploghttp.Compression {
	if cfg.OTEL.Compression == config.OTELCompressionNone {
		return otlploghttp.NoCompression
	}
	return otlploghttp.GzipCompression
}

// shutdown drains the queued records and stops the exporter.
func (s *otlpSink) shutdown(ctx context.Context) error {
	if err := s.provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("logger: otlp shutdown: %w", err)
	}
	return nil
}
