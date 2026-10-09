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

    it('falls back to a plain sentence for unknown shapes', () => {
      expect(getErrorMessage('boom')).toBe('An unexpected error occurred')
      expect(getErrorMessage(new Error('disk full'))).toBe('disk full')
    })
  })
})
