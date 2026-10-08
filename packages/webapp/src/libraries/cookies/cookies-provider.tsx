import { useMemo, type PropsWithChildren } from 'react'
import { Cookies } from './cookies'
import { CookiesContext } from './cookies-context'
import type { CookieSetOptions } from './types'

interface CookiesProviderProps extends PropsWithChildren {
  /** Injection point — tests and isolated trees pass their own instance. */
  cookies?: Cookies
  /** Applied when this provider mints its own instance. */
  defaultSetOptions?: CookieSetOptions
}

/**
 * Fills the cookies context. The SPA has no server render (the Go shell owns
 * the document), so there are no server-side cookie props to accept — the
 * reference implementation's SSR surface is deliberately not carried.
 */
export function CookiesProvider({ children, cookies, defaultSetOptions }: CookiesProviderProps) {
  const instance = useMemo(
    () => cookies ?? new Cookies(defaultSetOptions),
    [cookies, defaultSetOptions]
  )
  return <CookiesContext.Provider value={instance}>{children}</CookiesContext.Provider>
}
