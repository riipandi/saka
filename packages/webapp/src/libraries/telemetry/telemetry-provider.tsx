import { useEffect } from 'react'
import { useAppConfig } from '#/hooks/use-app-config'
import { configureTelemetry } from './telemetry'

/**
 * Mounts in the root stack beside the other providers and starts the web
 * tracer when the deployment document names a collector. Renders nothing:
 * the component is the schedule, not a UI element.
 *
 * The init waits for the document (the boot already prefetches it) and for
 * an idle tick after first paint, so the SDK's weight never lands on the
 * first load. With no endpoint the component stays inert — no provider, no
 * exporter, no queue.
 */
export function TelemetryProvider() {
  const { data } = useAppConfig()

  useEffect(() => {
    const start = () => {
      if (data) {
        const browser = data.otel.browser
        if (browser.endpoint) {
          void configureTelemetry({
            endpoint: browser.endpoint,
            ratio: browser.ratio,
            environment: data.app.mode,
            version: import.meta.env.PUBLIC_APP_VERSION ?? 'dev'
          })
        }
      }
    }
    let cancel: (() => void) | undefined
    if (typeof requestIdleCallback === 'function') {
      const id = requestIdleCallback(start)
      cancel = () => cancelIdleCallback(id)
    } else {
      const id = setTimeout(start, 1)
      cancel = () => clearTimeout(id)
    }
    // The document is immutable per process; a refetch cannot change the
    // fact, and configureTelemetry is idempotent when it does.
    return () => cancel?.()
  }, [data])

  return null
}
