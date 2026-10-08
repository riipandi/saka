import { authStore, clearAuth, setAuthLoading, setAuthUser } from './auth-store'
import { authWorker } from './auth-worker-client'

let bootPromise: Promise<void> | null = null

/**
 * Restore the session once per app lifecycle. Route guards (`beforeLoad`)
 * await this promise, so navigation never races the restore.
 *
 * The bootstrap is what makes a reload keep its session: the cookie-restored
 * pair is handed to the worker and the profile is rebuilt from the session
 * the access token names (`SessionService/GetSession`) — a failed or absent
 * pair leaves the signed-out shell, which is exactly what the route guard
 * needs to see.
 */
export function ensureSessionLoaded(): Promise<void> {
  if (!bootPromise) bootPromise = bootstrap()
  return bootPromise
}

async function bootstrap() {
  setAuthLoading(true)
  try {
    const worker = authWorker()
    await worker.restore()
    const profile = await worker.session()
    if (profile) {
      setAuthUser(profile)
    } else {
      clearAuth()
    }
  } catch {
    clearAuth()
  } finally {
    setAuthLoading(false)
  }
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
