import { useEffect, useMemo, useState } from 'react'
import { useCookiesInstance } from './cookies-context'
import type { CookieGetOptions, CookieSetOptions } from './types'

/**
 * The reference hook's surface: `[cookies, setCookie, removeCookie,
 * updateCookies]`. Re-renders fire only when a watched dependency moved —
 * an unwatched cookie's change keeps the component untouched.
 *
 * The comparison runs inside the state updater, so one subscription serves
 * every change without re-subscribing on each render.
 */
export function useCookies(
  dependencies?: string[],
  options?: CookieGetOptions
): [
  Record<string, unknown>,
  (name: string, value: string | object, options?: CookieSetOptions) => void,
  (name: string, options?: CookieSetOptions) => void,
  () => void
] {
  const cookies = useCookiesInstance()
  const [allCookies, setAllCookies] = useState<Record<string, unknown>>(() =>
    cookies.getAll(options)
  )

  useEffect(() => {
    const deps = dependencies ?? null
    const unsubscribe = cookies.addChangeListener(() => {
      const next = cookies.getAll(options)
      setAllCookies((prev) => (shouldUpdate(deps, next, prev) ? next : prev))
    })
    return unsubscribe
    // The watched names travel as a stable joined key — identity of the
    // array argument must not churn the subscription.
  }, [cookies, dependencies?.join(','), options?.doNotParse])

  // The writers are not bound to the watched list — a cookie a component
  // writes is often one it does not re-render on.
  const setCookie = useMemo(
    () => (name: string, value: string | object, setOptions?: CookieSetOptions) =>
      cookies.set(name, value, setOptions),
    [cookies]
  )
  const removeCookie = useMemo(
    () => (name: string, removeOptions?: CookieSetOptions) => cookies.remove(name, removeOptions),
    [cookies]
  )
  const updateCookies = useMemo(() => () => cookies.update(), [cookies])

  return [allCookies, setCookie, removeCookie, updateCookies]
}

/** Only a watched dependency's change earns a re-render; no list watches all. */
function shouldUpdate(
  dependencies: readonly string[] | null,
  next: Record<string, unknown>,
  prev: Record<string, unknown>
): boolean {
  if (!dependencies) return true
  return dependencies.some((name) => next[name] !== prev[name])
}
