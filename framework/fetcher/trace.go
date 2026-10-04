package fetcher

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// w3cPropagator is what an outbound call injects. It matches the propagator
// observer.SetGlobals installs. The SDK default carries no fields, so a call
// made before that installation still forwards a trace.
var w3cPropagator = propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{},
	propagation.Baggage{},
)

// startSpan records the outbound call as a client span and returns the
// context the headers are injected from. A tracer that is not recording
// keeps the parent span context, so a trace handed to this process is
// forwarded even when this process is not exporting spans.
func startSpan(ctx context.Context, scope, method, target string) (context.Context, trace.Span) {
	ctx, span := otel.Tracer(scope).Start(ctx, method+" "+target,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("http.request.method", method)),
	)
	return ctx, span
}

// injectTrace writes the W3C trace headers onto header. A caller-supplied
// traceparent is replaced by the span this call created.
func injectTrace(ctx context.Context, header http.Header) {
	prop := otel.GetTextMapPropagator()
	if len(prop.Fields()) == 0 {
		prop = w3cPropagator
	}
	prop.Inject(ctx, propagation.HeaderCarrier(header))
}

// finishSpan records the outcome. The error text is the sanitized fetcher
// error, which has no query string and no body.
func finishSpan(span trace.Span, status int, err error) {
	if status > 0 {
		span.SetAttributes(attribute.Int("http.response.status_code", status))
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
