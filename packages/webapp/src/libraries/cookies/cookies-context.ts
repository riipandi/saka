import { createContext, useContext } from 'react'
import type { Cookies } from './cookies'

/** The context the provider fills; `null` means no provider mounted. */
export const CookiesContext = createContext<Cookies | null>(null)

/** The instance the calling component reads and writes through. */
export function useCookiesInstance(): Cookies {
  const cookies = useContext(CookiesContext)
  if (!cookies) throw new Error('Missing <CookiesProvider>')
  return cookies
}
