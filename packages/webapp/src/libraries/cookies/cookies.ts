/**
 * The `Cookies` class — the read/write layer over `document.cookie`, adopted
 * from react-cookie's surface (get/getAll/set/remove/change listeners) and
 * implemented over `cookie-es` `parse`/`serialize`.
 *
 * `document.cookie` carries no change events, so change detection is the
 * reference design's: a serialized-jar comparison, run on every write through
 * this instance and on a one-second interval while a listener is attached.
 * Listeners are only started when somebody subscribes — an idle instance
 * costs no timer.
 */
import { parse, serialize } from 'cookie-es'
import type { CookieGetOptions, CookieSetOptions } from './types'

export type CookieChangeListener = () => void

/** How often the jar is re-read while listeners are attached. */
const CHANGE_DETECTION_INTERVAL_MS = 1000

/** Non-string values travel as JSON, matching the reference set semantics. */
function parseValue(value: string, doNotParse: boolean): unknown {
  if (doNotParse) return value
  try {
    return JSON.parse(value)
  } catch {
    return value
  }
}

export class Cookies {
  private defaultSetOptions: CookieSetOptions | undefined
  private listeners = new Set<CookieChangeListener>()
  private ticker: ReturnType<typeof setInterval> | null = null
  /** The last jar string this instance saw — the change-detection baseline. */
  private lastJar = ''

  constructor(defaultSetOptions?: CookieSetOptions) {
    this.defaultSetOptions = defaultSetOptions
    this.lastJar = this.readJarString()
  }

  /** One cookie's value, or `undefined` when absent. */
  get(name: string, options?: CookieGetOptions): unknown {
    const value = parse(this.readJarString())[name]
    if (value === undefined) return undefined
    return parseValue(value, options?.doNotParse === true)
  }

  /** The whole jar as an object keyed by cookie name. */
  getAll(options?: CookieGetOptions): Record<string, unknown> {
    const jar = parse(this.readJarString())
    const doNotParse = options?.doNotParse === true
    return Object.fromEntries(
      Object.entries(jar).map(([k, v]) => [k, parseValue(v ?? '', doNotParse)])
    )
  }

  /**
   * Write one cookie. Object values are JSON-stringified by `serialize`;
   * the write goes through the change detection, so attached listeners see
   * it in the same tick.
   */
  set(name: string, value: string | object, options?: CookieSetOptions): void {
    const merged = { ...this.defaultSetOptions, ...options }
    document.cookie = serialize(name, value, merged)
    this.sync()
  }

  /** Remove one cookie — a past expiry is the removal primitive. */
  remove(name: string, options?: CookieSetOptions): void {
    const merged = { ...this.defaultSetOptions, ...options, expires: new Date(0) }
    document.cookie = serialize(name, '', merged)
    this.sync()
  }

  /** Subscribe to jar changes; returns the unsubscribe. */
  addChangeListener(listener: CookieChangeListener): () => void {
    this.listeners.add(listener)
    this.startTicker()
    return () => this.removeChangeListener(listener)
  }

  removeChangeListener(listener: CookieChangeListener): void {
    this.listeners.delete(listener)
    if (this.listeners.size === 0) this.stopTicker()
  }

  /**
   * Re-read the jar and notify the listeners when it moved. The interval
   * tick calls this; so can a caller that just changed a cookie outside
   * this instance (the guard's writer does, in Phase 2).
   */
  update(): void {
    this.sync()
  }

  private readJarString(): string {
    return typeof document === 'undefined' ? '' : document.cookie
  }

  private sync(): void {
    const jar = this.readJarString()
    if (jar === this.lastJar) return
    this.lastJar = jar
    for (const listener of this.listeners) listener()
  }

  private startTicker(): void {
    if (this.ticker || typeof setInterval === 'undefined') return
    this.ticker = setInterval(() => this.sync(), CHANGE_DETECTION_INTERVAL_MS)
  }

  private stopTicker(): void {
    if (!this.ticker) return
    clearInterval(this.ticker)
    this.ticker = null
  }
}

/**
 * The shared instance non-React consumers use (the guard's persistence in
 * Phase 2 reads and writes through it). The provider accepts its own
 * instance for injection, so tests and isolated trees stay independent.
 */
export const cookies = new Cookies()
