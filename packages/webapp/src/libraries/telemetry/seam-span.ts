import {
  context,
  propagation,
  SpanKind,
  SpanStatusCode,
  trace,
  type Span
} from '@opentelemetry/api'
import {
  ATTR_HTTP_REQUEST_METHOD,
  ATTR_HTTP_RESPONSE_STATUS_CODE,
  ATTR_URL_FULL,
  ATTR_URL_PATH
} from '@opentelemetry/semantic-conventions'
import { webTracer } from './telemetry'

/** The trace context a caller hands across a boundary — from the main
 * thread's operation span into the worker realm's request headers. */
export interface TraceHints {
  traceparent?: string
}

/** The W3C carrier a span injects: empty while the tracer is off, which is
 * what makes an uninstrumented request cost nothing. */
function carrierFrom(span: Span): Record<string, string> {
  const carrier: Record<string, string> = {}
  if (span.isRecording()) propagation.inject(trace.setSpan(context.active(), span), carrier)
  return carrier
}

/**
 * One client span per HTTP request the seam sends — the instrumentation
 * point the REST fetcher and the Connect transport share, so both arrive at
 * the collector described by the same conventions. The span injects
 * `traceparent` from its own context, so the server's span is its child and
 * a trace the backend already sampled stays whole across the wire.
 *
 * A request that never rides the seam — the exporter's own POST to the
 * collector — never gets a span, and the redacting exporter drops any that
 * would describe it again.
 */
export async function sendWithSeamSpan(
  describe: { url: string; method?: string },
  send: (headers: Record<string, string>) => Promise<Response>
): Promise<Response> {
  const method = (describe.method ?? 'GET').toUpperCase()
  const target = new URL(describe.url, window.location.origin)

  const span = webTracer('saka-web-seam').startSpan(`HTTP ${method}`, {
    kind: SpanKind.CLIENT,
    attributes: {
      [ATTR_HTTP_REQUEST_METHOD]: method,
      [ATTR_URL_FULL]: target.toString(),
      [ATTR_URL_PATH]: target.pathname
    }
  })

  // The traceparent carries the span's own context, not the ambient one:
  // the server answers to the request span, whatever opened it.
  const headers = carrierFrom(span)

  try {
    const response = await send(headers)
    span.setAttribute(ATTR_HTTP_RESPONSE_STATUS_CODE, response.status)
    if (!response.ok)
      span.setStatus({ code: SpanStatusCode.ERROR, message: `HTTP ${response.status}` })
    span.end()
    return response
  } catch (cause) {
    span.setStatus({ code: SpanStatusCode.ERROR })
    if (cause instanceof Error) span.recordException(cause)
    else span.recordException(new Error(String(cause)))
    span.end()
    throw cause
  }
}

/**
 * One span around a whole operation — the unit of work a user action
 * causes — whose context travels with the operation's requests. The caller
 * passes the hints into whatever boundary the work crosses, so the server's
 * span is this span's child and the trace stays whole.
 */
export async function spanOperation<T>(
  name: string,
  run: (hints?: TraceHints) => Promise<T>
): Promise<T> {
  const span = webTracer('saka-web-auth').startSpan(`auth ${name}`)
  const carrier = carrierFrom(span)
  const hints: TraceHints | undefined = carrier.traceparent
    ? { traceparent: carrier.traceparent }
    : undefined
  try {
    const result = await run(hints)
    span.end()
    return result
  } catch (cause) {
    span.setStatus({ code: SpanStatusCode.ERROR })
    if (cause instanceof Error) span.recordException(cause)
    else span.recordException(new Error(String(cause)))
    span.end()
    throw cause
  }
}
