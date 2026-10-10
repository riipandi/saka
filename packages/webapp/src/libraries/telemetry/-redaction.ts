import { ExportResultCode } from '@opentelemetry/core'
import type { ReadableSpan, SpanExporter } from '@opentelemetry/sdk-trace-web'

/** The URL attributes whose value may carry the query a request was made with. */
const QUERY_BEARING_URL_KEYS = ['url.full', 'url.original', 'url.path'] as const

/** The URL attributes that are a query or a fragment by definition. */
const QUERY_ONLY_URL_KEYS = ['url.query', 'url.fragment'] as const

/** Strip everything from the first `?` or `#` — the query is where the mailed
 * tokens and the flow tokens travel, and none of them may leave the page. */
export function stripQuery(url: string): string {
  const cut = url.search(/[?#]/)
  return cut === -1 ? url : url.slice(0, cut)
}

function originOf(value: string): string | null {
  try {
    return new URL(value).origin
  } catch {
    return null
  }
}

/** Drop the query and the query-only attributes off a span's URL facts. A
 * span without any of them is returned untouched, so the common case costs
 * one key scan. */
export function redactSpan(span: ReadableSpan): ReadableSpan {
  const attributes = { ...span.attributes }
  let changed = false
  for (const key of QUERY_ONLY_URL_KEYS) {
    if (key in attributes) {
      delete attributes[key]
      changed = true
    }
  }
  for (const key of QUERY_BEARING_URL_KEYS) {
    const value = attributes[key]
    if (typeof value === 'string' && /[?#]/.test(value)) {
      attributes[key] = stripQuery(value)
      changed = true
    }
  }
  return changed ? { ...span, attributes } : span
}

export interface RedactionRules {
  /** The origin the page is served from — a URL outside it never exports. */
  ownOrigin: string
  /** The collector's own URL, dropped even when it answers on this origin. */
  telemetryEndpoint?: string
}

function isDropped(span: ReadableSpan, rules: RedactionRules): boolean {
  for (const key of [...QUERY_BEARING_URL_KEYS, ...QUERY_ONLY_URL_KEYS]) {
    const value = span.attributes[key]
    if (typeof value !== 'string') continue
    if (rules.telemetryEndpoint && stripQuery(value) === stripQuery(rules.telemetryEndpoint))
      return true
    const origin = originOf(value)
    // A relative URL has no origin of its own — it answers to this page.
    if (origin !== null && origin !== rules.ownOrigin) return true
  }
  return false
}

/**
 * The export path's one guard: before any span leaves the page, the query is
 * stripped off its URL facts and a span that describes a request this page
 * never owned — the telemetry endpoint itself, any third-party origin — is
 * dropped entirely. Written once here rather than at every span site, so a
 * future instrumentation inherits it by riding the same exporter.
 */
export function createRedactingExporter(next: SpanExporter, rules: RedactionRules): SpanExporter {
  return {
    export(spans, resultCallback) {
      const kept: ReadableSpan[] = []
      for (const span of spans) {
        if (!isDropped(span, rules)) kept.push(redactSpan(span))
      }
      if (kept.length === 0) {
        resultCallback({ code: ExportResultCode.SUCCESS })
        return
      }
      next.export(kept, resultCallback)
    },
    shutdown: () => next.shutdown(),
    forceFlush: () => (next.forceFlush ? next.forceFlush() : Promise.resolve())
  }
}
