import { ConnectError } from '@connectrpc/connect'
import { Code } from '@connectrpc/connect'
import { describe, expect, it } from 'vite-plus/test'
import { getErrorMessage, safeReturnTo } from '#/libraries/guard/auth-utils'

describe('auth utils', () => {
  describe('safeReturnTo (the open-redirect guard)', () => {
    it('admits same-origin relative paths only', () => {
      expect(safeReturnTo('/overview')).toBe('/overview')
      expect(safeReturnTo('/overview?next=/settings')).toBe('/overview?next=/settings')
    })

    it('refuses absolute and protocol-relative targets', () => {
      expect(safeReturnTo('https://evil.com')).toBeNull()
      expect(safeReturnTo('//evil.com')).toBeNull()
      // The backslash spelling some URL parsers read as a protocol-relative //.
      expect(safeReturnTo('/\\evil.com')).toBeNull()
      expect(safeReturnTo('overview')).toBeNull()
      expect(safeReturnTo(undefined)).toBeNull()
      expect(safeReturnTo('')).toBeNull()
    })
  })

  describe('getErrorMessage', () => {
    it('reads a Connect error as the user-facing contract message', () => {
      const error = new ConnectError('Invalid credentials.', Code.Unauthenticated)
      expect(getErrorMessage(error)).toBe('Invalid credentials.')
    })

    it('reads a bare refusal as its code word, not a code name', () => {
      // The server's sentence always wins; the table is what an empty
      // refusal reads as.
      const cases: Array<[Code, string]> = [
        [Code.AlreadyExists, 'That value is already taken.'],
        [Code.NotFound, 'That item no longer exists.'],
        [Code.FailedPrecondition, 'That action is not available right now.'],
        [Code.PermissionDenied, 'You do not have permission to do that.'],
        [Code.ResourceExhausted, 'Too many attempts. Try again in a moment.']
      ]
      for (const [code, word] of cases) {
        expect(getErrorMessage(new ConnectError('', code))).toBe(word)
      }
    })

    it('falls back to the plain sentence for a code with no word', () => {
      expect(getErrorMessage(new ConnectError('', Code.DataLoss))).toBe(
        'An unexpected error occurred'
      )
    })

    it('falls back to a plain sentence for unknown shapes', () => {
      expect(getErrorMessage('boom')).toBe('An unexpected error occurred')
      expect(getErrorMessage(new Error('disk full'))).toBe('disk full')
    })
  })
})
