import { renderHook, act } from '@testing-library/react'
import { parse } from 'cookie-es'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it } from 'vite-plus/test'
import { Cookies } from '#/libraries/cookies/cookies'
import { CookiesProvider } from '#/libraries/cookies/cookies-provider'
import { useCookies } from '#/libraries/cookies/use-cookies'

function clearJar(): void {
  for (const name of Object.keys(parse(document.cookie))) {
    document.cookie = `${name}=; max-age=0; expires=Thu, 01 Jan 1970 00:00:00 GMT`
    document.cookie = `${name}=; path=/; max-age=0; expires=Thu, 01 Jan 1970 00:00:00 GMT`
  }
}

/** An isolated jar per test — no cross-test cookie bleed. */
function wrapper(): (props: { children: ReactNode }) => ReactNode {
  const jar = new Cookies({ path: '/' })
  return ({ children }) => <CookiesProvider cookies={jar}>{children}</CookiesProvider>
}

describe('useCookies', () => {
  afterEach(() => {
    clearJar()
  })
  it('exposes the current jar', () => {
    const { result } = renderHook(() => useCookies(), { wrapper: wrapper() })
    expect(result.current[0]).toEqual({})
  })

  it('sees a value the hook itself wrote', () => {
    const { result } = renderHook(() => useCookies(), { wrapper: wrapper() })
    const [, setCookie] = result.current

    act(() => setCookie('lang', 'id'))
    expect(result.current[0]).toEqual({ lang: 'id' })
  })

  it('round-trips an object value', () => {
    const { result } = renderHook(() => useCookies(), { wrapper: wrapper() })
    const [, setCookie] = result.current

    act(() => setCookie('prefs', { theme: 'dark' }))
    expect(result.current[0].prefs).toEqual({ theme: 'dark' })
  })

  it('clears the value on removeCookie', () => {
    const { result } = renderHook(() => useCookies(), { wrapper: wrapper() })
    const [, setCookie, removeCookie] = result.current

    act(() => setCookie('lang', 'id'))
    act(() => removeCookie('lang'))
    expect(result.current[0]).toEqual({})
  })

  it('re-renders when a watched dependency changes', () => {
    const { result } = renderHook(() => useCookies(['lang']), { wrapper: wrapper() })
    const [, setCookie] = result.current

    act(() => setCookie('lang', 'id'))
    expect(result.current[0].lang).toBe('id')
  })

  it('does not re-render when an unwatched cookie changes', () => {
    const { result } = renderHook(() => useCookies(['watched']), { wrapper: wrapper() })
    const [, setCookie] = result.current

    act(() => setCookie('unwatched', 'x'))
    // No re-render happened, so the hook's view of the jar is stale by
    // exactly the cookie it does not watch.
    expect(result.current[0]).toEqual({})
    expect(result.current[0].unwatched).toBeUndefined()
  })

  it('throws without a provider', () => {
    expect(() => renderHook(() => useCookies())).toThrow(/Missing <CookiesProvider>/)
  })
})
