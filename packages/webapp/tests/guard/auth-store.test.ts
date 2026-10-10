import { beforeEach, describe, expect, it } from 'vite-plus/test'
import { authStore, clearAuth, setAuthGrants, setAuthUser } from '#/libraries/guard/auth-store'
import { userJson } from './auth-connect-mock'

describe('auth store grants', () => {
  beforeEach(() => {
    clearAuth()
  })

  it('boots with the empty snapshot', () => {
    expect(authStore.state.grants).toEqual({ roles: [], permissions: [] })
  })

  it('replaces the snapshot wholesale, never merging', () => {
    setAuthGrants({ roles: ['administrator'], permissions: ['user:*:read'] })

    setAuthGrants({ roles: ['viewer'], permissions: ['user:usr_1:read'] })

    expect(authStore.state.grants).toEqual({
      roles: ['viewer'],
      permissions: ['user:usr_1:read']
    })
  })

  it('null is the signed-out shape', () => {
    setAuthGrants({ roles: ['viewer'], permissions: ['user:*:read'] })

    setAuthGrants(null)

    expect(authStore.state.grants).toEqual({ roles: [], permissions: [] })
  })

  it('clearAuth drops the grants beside the profile', () => {
    setAuthUser(userJson)
    setAuthGrants({ roles: ['administrator'], permissions: ['user:*:read'] })

    clearAuth()

    expect(authStore.state.user).toBeNull()
    expect(authStore.state.grants).toEqual({ roles: [], permissions: [] })
  })
})
