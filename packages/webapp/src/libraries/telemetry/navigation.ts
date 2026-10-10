import { SpanStatusCode, type Span } from '@opentelemetry/api'
import { ATTR_URL_PATH } from '@opentelemetry/semantic-conventions'
import { webTracer } from './telemetry'

/**
 * The slice of the router the wiring reads: the lifecycle events and the
 * settled matches. Structural, so a test can drive it with a fake and the
 * real router satisfies it without a cast.
 */
interface NavigationRouter {
  subscribe(
    eventType: 'onBeforeLoad' | 'onLoad' | 'onResolved',
    listener: (event: { toLocation: { pathname: string } }) => void
  ): () => void
  state: { matches: ReadonlyArray<{ routeId?: string; status?: string }> }
}

/**
 * One span per navigation: opened when the router begins its load, named by
 * the resolved route id, and ended when the route answers. The requests the
 * navigation fires ride `authFetch`, so the seam's client spans become this
 * span's children and the trace shows the whole turn.
 *
 * A navigation that is superseded by the next one (a redirect in a
 * `beforeLoad` starts the target before this one resolved) ends silently —
 * the redirect is not a failure. A load that ended with an errored match
 * ends as an error.
 */
export function wireNavigationSpans(router: NavigationRouter): () => void {
  const tracer = webTracer('saka-web-nav')
  let current: Span | null = null

  const supersede = () => {
    if (!current) return
    current.end()
    current = null
  }

  const offBeforeLoad = router.subscribe('onBeforeLoad', (event) => {
    supersede()
    current = tracer.startSpan(`nav ${event.toLocation.pathname}`, {
      attributes: { [ATTR_URL_PATH]: event.toLocation.pathname }
    })
  })

  const offLoad = router.subscribe('onLoad', () => {
    const errored = router.state.matches.some((match) => match.status === 'error')
    if (current && errored) {
      current.setStatus({ code: SpanStatusCode.ERROR, message: 'navigation failed' })
    }
  })

  const offResolved = router.subscribe('onResolved', () => {
    if (!current) return
    const routeId = router.state.matches.at(-1)?.routeId
    if (routeId) current.updateName(`nav ${routeId}`)
    current.end()
    current = null
  })

  return () => {
    supersede()
    offBeforeLoad()
    offLoad()
    offResolved()
  }
}
