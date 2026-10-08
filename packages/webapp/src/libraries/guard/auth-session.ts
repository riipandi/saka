import { readUserCookie } from './auth-cookies'
import { authStore, clearAuth, setAuthLoading, setAuthUser, type UserProfile } from './auth-store'
import { authWorker, type AuthWorkerClient } from './auth-worker-client'

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
    if (cached) {
      setAuthUser(cached)
      setAuthLoading(false)
      void verifyInBackground(worker, cached)
      return
    }
    try {
      const profile = await worker.session()
      if (profile) setAuthUser(profile)
      else clearAuth()
    } catch {
      // Inconclusive — the backend never answered. With no cached profile
      // there is nothing to show but the signed-out shell.
      clearAuth()
    }
    setAuthLoading(false)
  })
  return bootPromise
}

/**
 * The background verify that the user never waits for. A confirmed profile
 * replaces the cached one — but only while the boot account is still the one
 * displayed: a logout or a re-login that raced the verify must not be
 * resurrected by a stale answer. An inconclusive failure (the backend never
 * answered) keeps the cached account on screen; a definite `null` ends the
 * session the cookies claimed.
 */
async function verifyInBackground(worker: AuthWorkerClient, cached: UserProfile): Promise<void> {
  const stillBootAccount = () => authStore.state.user?.id === cached.id
  try {
    const profile = await worker.session()
    if (profile) {
      if (stillBootAccount()) setAuthUser(profile)
    } else if (stillBootAccount()) {
      clearAuth()
    }
  } catch {
    // Offline or unreachable — show the cached account until a real answer.
  }
}

/**
 * Proactive refresh for tab-focus events. Clears the session state only when
 * the pair is truly gone — a refused refresh drops it in the worker, while a
 * network failure leaves it in place for the next retry.
 */
export async function refreshIfExpiring(withinMs: number): Promise<void> {
  if (!authStore.state.user) return
  const ok = await authWorker().maybeRefresh(withinMs)
  if (!ok && !(await authWorker().accessToken())) clearAuth()
}
