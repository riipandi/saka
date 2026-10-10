import { beforeEach, describe, expect, it } from 'vite-plus/test'
import { authStore, clearAuth, setAuthGrants } from '#/libraries/guard/auth-store'
import { can, isAdministrator } from '#/libraries/guard/permissions-store'

describe('the store-backed permission read', () => {
  beforeEach(() => {
    clearAuth()
  })

  it('answers from the snapshot the store holds', () => {
    setAuthGrants({ roles: ['viewer'], permissions: ['user:*:read', 'session:sess_1:end'] })

    expect(can('user:usr_9:read')).toBe(true)
    expect(can('user:usr_1:read')).toBe(true)
    expect(can('user:usr_1:ban')).toBe(false)
    expect(can('session:sess_1:end')).toBe(true)
    expect(can('session:sess_2:end')).toBe(false)
  })

  it('answers nothing for the signed-out snapshot', () => {
    expect(can('user:*:read')).toBe(false)
    expect(isAdministrator()).toBe(false)
  })

  it('refuses a malformed requirement the matcher rejects', () => {
    setAuthGrants({ roles: ['administrator'], permissions: ['user:*:read'] })

    expect(can('user:list')).toBe(false)
    expect(can('')).toBe(false)
  })

  it('names the administrator by the role the snapshot carries', () => {
    setAuthGrants({ roles: ['administrator'], permissions: [] })
    expect(isAdministrator()).toBe(true)

    setAuthGrants({ roles: ['viewer', 'curator'], permissions: [] })
    expect(isAdministrator()).toBe(false)

    setAuthGrants({ roles: [], permissions: ['user:*:read'] })
    expect(isAdministrator()).toBe(false)
  })

  it('follows the store live between reads', () => {
    expect(can('user:*:read')).toBe(false)

    setAuthGrants({ roles: [], permissions: ['user:*:read'] })
    expect(can('user:*:read')).toBe(true)

    clearAuth()
    expect(can('user:*:read')).toBe(false)
    expect(authStore.state.grants.permissions).toEqual([])
  })
})
