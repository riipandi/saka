import { ConnectError } from '@connectrpc/connect'
import { Code } from '@connectrpc/connect'
import { FetchError } from 'ofetch'

/**
 * The user-facing word for a Connect code the server answers without a
 * sentence of its own. The server's `rawMessage` is the contract and always
 * wins; this table is the fallback so an empty refusal still reads as
 * something a person understands instead of a code name.
 */
const CODE_MESSAGES: Partial<Record<Code, string>> = {
  [Code.AlreadyExists]: 'That value is already taken.',
  [Code.NotFound]: 'That item no longer exists.',
  [Code.FailedPrecondition]: 'That action is not available right now.',
  [Code.PermissionDenied]: 'You do not have permission to do that.',
  [Code.ResourceExhausted]: 'Too many attempts. Try again in a moment.'
}

/**
 * Extract a human-readable message from an error. Handles Connect errors (the
 * RPC contract reports failures as connect codes with a detail message),
 * `ofetch` FetchError shapes, standard Errors, and fallback text.
 */
export function getErrorMessage(error: unknown): string {
  if (error instanceof ConnectError) {
    // `message` carries the "[unauthenticated]" code suffix developer tools
    // want; the user-facing surface reads the raw sentence alone, and a
    // refusal that arrived without one reads as the code's word.
    return error.rawMessage || CODE_MESSAGES[error.code] || 'An unexpected error occurred'
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
