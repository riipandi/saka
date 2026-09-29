// Exporter plumbing shared by every OTLP exporter this process builds.
//
// internal/observer builds three exporters, and internal/logger builds the
// fourth — the log sink — against the same collector. The settings that close
// the environment door are the same for all of them, so they live here once,
// and a decision about what an exporter may read from the environment cannot
// drift between the packages that depend on it.
//
// The rule the helpers carry out: the resolved configuration is the only source
// of truth for where telemetry goes. The exporters otherwise read
// OTEL_EXPORTER_OTLP_* on their own — endpoint, headers, compression, TLS
// material — and a stray export in a shell could redirect a signal, inject a
// header, or trust a certificate the config file never mentioned.
//
// Every exporter travels over http/protobuf; there is no gRPC leg to configure.

package observer

import (
	"crypto/tls"
)

// TLSConfig returns the TLS configuration for an HTTP collector endpoint.
//
// A nil configuration is not the same as leaving the option out: passing nil is
// what stops the exporter from loading OTEL_EXPORTER_OTLP_CERTIFICATE and
// friends, which would be a second source deciding what this process trusts. A
// secure endpoint gets the floor of TLS 1.2 and the system's root certificates,
// because the config file names no certificate of its own.
func TLSConfig(secure bool) *tls.Config {
	if secure {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return nil
}

// SignalPath returns the route a signal takes on the collector, or empty when
// the endpoint already names one.
//
// The configured path is the explicit answer and wins. Without it, an endpoint
// that carries a path keeps it — a collector mounted under a prefix, or a
// backend whose route is not the protocol's — and a bare host gets the
// protocol's own route.
func SignalPath(configured, endpointPath, fallback string) string {
	if configured != "" {
		return configured
	}
	if endpointPath != "" {
		return ""
	}
	return fallback
}
