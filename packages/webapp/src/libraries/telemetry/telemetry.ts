import { propagation, trace } from '@opentelemetry/api'
import { W3CTraceContextPropagator } from '@opentelemetry/core'
import { OTLPTraceExporter } from '@opentelemetry/exporter-trace-otlp-http'
import { resourceFromAttributes } from '@opentelemetry/resources'
import {
  BatchSpanProcessor,
  ParentBasedSampler,
  TraceIdRatioBasedSampler,
  WebTracerProvider,
  type SpanExporter
} from '@opentelemetry/sdk-trace-web'
import {
  ATTR_DEPLOYMENT_ENVIRONMENT_NAME,
  ATTR_SERVICE_NAME,
  ATTR_SERVICE_VERSION
} from '@opentelemetry/semantic-conventions'
import { createRedactingExporter } from './-redaction'

/**
 * The web tracer's initialization. The configuration document is the only
 * source: an empty endpoint means frontend tracing does not exist, and a
 * configured one is the address the SPA exports to, cross-origin, with the
 * collector's receiver answering the CORS check.
 *
 * Every signal below the export is queued and drained in the background —
 * a span is handed to the batch processor and the caller returns, so a slow
 * or unreachable collector costs dropped spans, never a slow page.
 */

export interface WebTracingConfig {
  /** The collector address the browser exports to. Empty means off. */
  endpoint: string
  /** The fraction of traces sampled; zero samples everything. */
  ratio: number
  /** The deployment's environment, from the public document's `app.mode`. */
  environment: string
  /** The build the spans come from. */
  version: string
  /** Test seam: the batch's flush interval in milliseconds. */
  batchMillis?: number
}

let shutdownProvider: (() => Promise<void>) | null = null

/** Whether the web tracer is running. */
export function telemetryActive(): boolean {
  return shutdownProvider !== null
}

/** The sampler the configuration names. Zero means every trace — an operator
 * who named a collector wanted spans, and a silent zero would record nothing.
 * The parent-based form keeps a trace the backend already sampled whole. */
export function samplerFor(ratio: number): ParentBasedSampler {
  const root = new TraceIdRatioBasedSampler(ratio === 0 ? 1 : ratio)
  return new ParentBasedSampler({ root })
}

/** The exporter URL: the deployment's address with the protocol's own route
 * appended, unless the address already names a route. */
function exporterUrl(endpoint: string): string {
  if (/v1\/traces\/?$/.test(endpoint)) return endpoint
  return `${endpoint.replace(/\/$/, '')}/v1/traces`
}

/**
 * Build and register the tracer. Idempotent: a second call while the tracer
 * runs is a no-op, so a configuration refetch cannot stack providers.
 *
 * `exporter` is a test seam — the production exporter is the OTLP/HTTP one.
 * Resolves `true` when the tracer is running after the call.
 */
export async function configureTelemetry(
  config: WebTracingConfig,
  exporter?: SpanExporter
): Promise<boolean> {
  if (shutdownProvider) return true
  if (!config.endpoint) return false

  const provider = new WebTracerProvider({
    resource: resourceFromAttributes({
      [ATTR_SERVICE_NAME]: 'saka-web',
      [ATTR_SERVICE_VERSION]: config.version,
      ...(config.environment ? { [ATTR_DEPLOYMENT_ENVIRONMENT_NAME]: config.environment } : {})
    }),
    sampler: samplerFor(config.ratio),
    spanProcessors: [
      new BatchSpanProcessor(
        createRedactingExporter(
          exporter ?? new OTLPTraceExporter({ url: exporterUrl(config.endpoint) }),
          {
            ownOrigin: window.location.origin,
            telemetryEndpoint: exporterUrl(config.endpoint)
          }
        ),
        { scheduledDelayMillis: config.batchMillis ?? 5000, maxQueueSize: 2048 }
      )
    ]
  })
  provider.register()

  // The API's default propagator carries no fields, so an outbound call
  // would drop a trace it was given — the same reason the Go side sets its
  // own composite explicitly.
  propagation.setGlobalPropagator(new W3CTraceContextPropagator())

  shutdownProvider = () => provider.shutdown().then(() => void (shutdownProvider = null))
  return true
}

/** Flush and stop the tracer; resolves when the queue is drained. A call
 * while the tracer is off is a no-op. */
export async function shutdownTelemetry(): Promise<void> {
  if (!shutdownProvider) return
  const drain = shutdownProvider
  shutdownProvider = null
  await drain()
  // The no-op globals return once the provider is gone: asking the API for a
  // tracer after shutdown costs a nil check, the honest cost of a signal
  // nobody asked for.
  trace.disable()
}

/** The tracer a span site asks for. While the tracer is off this is the
 * API's no-op — recording a span then costs a nil check. */
export function webTracer(name = 'saka-web') {
  return trace.getTracer(name)
}
