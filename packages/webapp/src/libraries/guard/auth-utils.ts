import { ConnectError } from '@connectrpc/connect'
import { FetchError } from 'ofetch'

/**
 * Extract a human-readable message from an error. Handles Connect errors (the
 * RPC contract reports failures as connect codes with a detail message),
 * `ofetch` FetchError shapes, standard Errors, and fallback text.
 */
export function getErrorMessage(error: unknown): string {
  if (error instanceof ConnectError) {
    // `message` carries the "[unauthenticated]" code suffix developer tools
    // want; the user-facing surface reads the raw sentence alone.
    return error.rawMessage || error.message
  }
  if (error instanceof FetchError) {
    return error.data?.message ?? error.message
  }
  if (error instanceof Error) return error.message
  return 'An unexpected error occurred'
}

/**
 * Validate a `return_to` redirect target. Only same-origin relative paths
 * are allowed (must start with a single `/`) — blocks open redirects like
 * `//evil.com` or `https://evil.com`, including the backslash spelling some
 * URL parsers read as a protocol-relative `//`.
 */
export function safeReturnTo(value: string | undefined | null): string | null {
  if (!value || !value.startsWith('/') || value.startsWith('//') || value.startsWith('/\\')) {
    return null
  }
  return value
}
