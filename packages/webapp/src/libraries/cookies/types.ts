/**
 * Cookie attribute shapes, mirroring what `cookie-es` `serialize` accepts
 * (RFC 6265) and what the reference implementation (react-cookie) exposes.
 */

export interface CookieSetOptions {
  /** Path scope; `/` makes the cookie site-wide. */
  path?: string
  /** Absolute expiration; a past date removes the cookie. */
  expires?: Date
  /** Relative lifetime in seconds; takes precedence over `expires`. */
  maxAge?: number
  /** Domain the cookie applies to (host-only when absent). */
  domain?: string
  /** TLS-only delivery. */
  secure?: boolean
  /**
   * Accepted for API parity, but a JS-written cookie is always JS-readable —
   * browsers drop the attribute on `document.cookie` writes. HttpOnly is a
   * server-side property; see the guard's custody comments.
   */
  httpOnly?: boolean
  /** `true` is Strict; `'lax'`/`'none'` name the enforcement modes. */
  sameSite?: boolean | 'lax' | 'strict' | 'none'
  /** CHIPS partitioned storage. */
  partitioned?: boolean
  /** Cookie priority hint. */
  priority?: 'low' | 'medium' | 'high'
}

export interface CookieGetOptions {
  /** Return the raw string — no JSON attempt. */
  doNotParse?: boolean
}
