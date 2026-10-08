import { readUserCookie } from './auth-cookies'
import { authStore, clearAuth, setAuthLoading, setAuthUser } from './auth-store'
import { authWorker } from './auth-worker-client'

let bootPromise: Promise<void> | null = null

/**
 * Restore the session once per app lifecycle. Route guards (`beforeLoad`)
 * await this promise, so navigation never races the restore.
 *
 * The restore is instant when the reload has a cached profile: the cookie
 * pair is handed to the worker and the cookie profile is set on the store
 * with no network at all, so the guard admits the reload to the signed-in
 * shell immediately. The confirmed answer (`SessionService/GetSession`)
 * replaces the cached profile in the background — or ends the session the
 * cookies claimed. Without a cached profile there is nothing to show but
 * the truth, so the confirmed answer is awaited before the guard decides.
 */
export function ensureSessionLoaded(): Promise<void> {
  if (bootPromise) return bootPromise
  const worker = authWorker()
  setAuthLoading(true)
  bootPromise = worker.restore().then(async () => {
    const cached = readUserCookie()
    if (!cached) {
      const profile = await worker.session()
      if (profile) setAuthUser(profile)
      else clearAuth()
      setAuthLoading(false)
      return
    }
    setAuthUser(cached)
    setAuthLoading(false)
    // Verify in the background — the user never waits for this round trip.
    const profile = await worker.session().catch(() => null)
    if (profile) setAuthUser(profile)
    else clearAuth()
  })
  return bootPromise
}

/**
 * Proactive refresh for tab-focus events. Clears the session state when the
 * worker reports the pair can no longer be renewed.
 */
export async function refreshIfExpiring(withinMs: number): Promise<void> {
  if (!authStore.state.user) return
  const ok = await authWorker().maybeRefresh(withinMs)
  if (!ok) clearAuth()
}
