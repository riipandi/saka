import { z } from 'zod'
/**
 * Cross-tab session sync. The token pair lives in each tab's own worker, and
 * every tab holds its own cookie copy — without a bridge, a tab that missed a
 * rotation replays a spent refresh token and the backend revokes the session
 * for every tab. The custody-change listener publishes every pair it takes or
 * drops; the other tabs adopt the fresh pair (or end their session) instead
 * of ever touching the network with a dead token.
 *
 * The channel is main-thread only — like the cookies it mirrors — and the
 * echo suppression is keyed on the refresh token, the one value that changes
 * with every rotation: an adoption re-reports the adopted pair through the
 * engine's own listener, and the identical key stops it from bouncing back.
 */
import { tokenBundleSchema } from './auth-cookies'
import type { TokenBundle } from './auth-engine'

const CHANNEL_NAME = 'saka.auth.session'

const messageSchema = z.object({ tokens: tokenBundleSchema.nullable() })

let channel: BroadcastChannel | null = null
/** The refresh token this tab last published or adopted — the echo key. */
let lastSyncedRefresh: string | null = null

function open(): BroadcastChannel | null {
  if (channel) return channel
  if (typeof BroadcastChannel === 'undefined') return null
  channel = new BroadcastChannel(CHANNEL_NAME)
  return channel
}

/** Publish a custody change to the other tabs. */
export function publishTokens(tokens: TokenBundle | null): void {
  if (tokens?.refreshToken === lastSyncedRefresh) return
  lastSyncedRefresh = tokens?.refreshToken ?? null
  open()?.postMessage({ tokens })
}

/**
 * Subscribe to custody changes from the other tabs; returns the unsubscribe.
 * A dead pair is ignored — adopting it would only drop it again and report
 * the drop as a sign-out to every tab.
 */
export function subscribeTokens(handler: (tokens: TokenBundle | null) => void): () => void {
  const bus = open()
  if (!bus) return () => {}
  const listener = (event: MessageEvent) => {
    const parsed = messageSchema.safeParse(event.data)
    if (!parsed.success) return
    const tokens = parsed.data.tokens
    if (tokens?.refreshToken === lastSyncedRefresh) return
    if (tokens && tokens.refreshExpiresAt <= Date.now()) return
    lastSyncedRefresh = tokens?.refreshToken ?? null
    handler(tokens)
  }
  bus.addEventListener('message', listener)
  return () => bus.removeEventListener('message', listener)
}
