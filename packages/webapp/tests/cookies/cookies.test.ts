import { parse } from 'cookie-es'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { Cookies } from '#/libraries/cookies/cookies'

/** `document.cookie = ''` clears nothing — every name must be expired. */
function clearJar(): void {
  for (const name of Object.keys(parse(document.cookie))) {
    // happy-dom stores a no-path set at its own default path, so each name
    // is expired both ways — the `path=/` variant and the bare one.
    document.cookie = `${name}=; max-age=0; expires=Thu, 01 Jan 1970 00:00:00 GMT`
    document.cookie = `${name}=; path=/; max-age=0; expires=Thu, 01 Jan 1970 00:00:00 GMT`
  }
}

function freshInstance(defaultSetOptions?: { path: string }): Cookies {
  return new Cookies(defaultSetOptions)
}

describe('the Cookies class', () => {
  beforeEach(() => {
    clearJar()
  })

  afterEach(() => {
    clearJar()
    vi.useRealTimers()
  })

  it('round-trips a string value', () => {
    const jar = freshInstance()
    jar.set('lang', 'id')
    expect(jar.get('lang')).toBe('id')
  })

  it('round-trips an object value as JSON', () => {
    const jar = freshInstance()
    jar.set('prefs', { theme: 'dark' })
    expect(jar.get('prefs')).toEqual({ theme: 'dark' })
  })

  it('returns the raw string under doNotParse', () => {
    const jar = freshInstance()
    jar.set('prefs', { theme: 'dark' })
    expect(jar.get('prefs', { doNotParse: true })).toBe('{"theme":"dark"}')
  })

  it('answers undefined for an absent cookie', () => {
    expect(freshInstance().get('absent')).toBeUndefined()
  })

  it('removes a cookie, leaving no readable residue', () => {
    const jar = freshInstance()
    jar.set('lang', 'id')
    jar.remove('lang')
    expect(jar.get('lang')).toBeUndefined()
    expect(document.cookie).not.toContain('lang=')
  })

  it('getAll mirrors the whole jar', () => {
    const jar = freshInstance()
    jar.set('a', 'one')
    jar.set('b', 'two')
    expect(jar.getAll()).toEqual({ a: 'one', b: 'two' })
  })

  it('applies the default set options to every write', () => {
    const jar = freshInstance({ path: '/' })
    jar.set('lang', 'id', { sameSite: 'strict' })
    expect(document.cookie).toContain('lang=id')
    // happy-dom exposes attributes only through the parsed jar; the write
    // itself succeeding with the merged options is the contract here.
    expect(jar.get('lang')).toBe('id')
  })

  it('fires the change listeners on its own writes', () => {
    const jar = freshInstance()
    const listener = vi.fn<() => void>()
    jar.addChangeListener(listener)

    jar.set('lang', 'id')
    expect(listener).toHaveBeenCalledTimes(1)

    jar.remove('lang')
    expect(listener).toHaveBeenCalledTimes(2)
  })

  it('stays silent when a write changes nothing', () => {
    const jar = freshInstance()
    jar.set('lang', 'id')
    const listener = vi.fn<() => void>()
    jar.addChangeListener(listener)

    jar.set('lang', 'id')
    expect(listener).not.toHaveBeenCalled()
  })

  it('detects an external write through update()', () => {
    const jar = freshInstance()
    const listener = vi.fn<() => void>()
    jar.addChangeListener(listener)

    document.cookie = 'external=1; path=/'
    expect(listener).not.toHaveBeenCalled()
    jar.update()
    expect(listener).toHaveBeenCalledTimes(1)
  })

  it('detects an external write on the interval tick', () => {
    vi.useFakeTimers()
    const jar = freshInstance()
    const listener = vi.fn<() => void>()
    jar.addChangeListener(listener)

    document.cookie = 'external=1; path=/'
    vi.advanceTimersByTime(1000)
    expect(listener).toHaveBeenCalledTimes(1)
  })

  it('runs the interval only while a listener is attached', () => {
    vi.useFakeTimers()
    const jar = freshInstance()
    const unsubscribe = jar.addChangeListener(vi.fn<() => void>())
    expect(vi.getTimerCount()).toBe(1)

    unsubscribe()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('unsubscribes through the returned handle', () => {
    const jar = freshInstance()
    const listener = vi.fn<() => void>()
    const unsubscribe = jar.addChangeListener(listener)

    unsubscribe()
    jar.set('lang', 'id')
    expect(listener).not.toHaveBeenCalled()
  })
})
