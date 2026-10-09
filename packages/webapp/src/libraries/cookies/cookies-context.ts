import { createContext, useContext } from 'react'
import type { Cookies } from './cookies'

/** `null` means no provider mounted. */
export const CookiesContext = createContext<Cookies | null>(null)

export function useCookiesInstance(): Cookies {
  const cookies = useContext(CookiesContext)
  if (!cookies) throw new Error('Missing <CookiesProvider>')
  return cookies
}
