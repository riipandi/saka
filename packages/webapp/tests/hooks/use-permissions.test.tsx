import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vite-plus/test'
import { clearAuth, setAuthGrants } from '#/libraries/guard/auth-store'
import { usePermissions } from '#/libraries/guard/use-permissions'

describe('usePermissions', () => {
  it('reads the snapshot the store holds', () => {
    setAuthGrants({ roles: ['viewer'], permissions: ['user:*:read', 'session:sess_1:end'] })

    const { result } = renderHook(() => usePermissions())

    expect(result.current.can('user:usr_9:read')).toBe(true)
    expect(result.current.can('session:sess_1:end')).toBe(true)
    expect(result.current.can('session:sess_2:end')).toBe(false)
    expect(result.current.isAdministrator).toBe(false)
    clearAuth()
  })

  it('re-renders the reader when custody replaces the snapshot', () => {
    setAuthGrants({ roles: [], permissions: [] })

    const { result } = renderHook(() => usePermissions())
    expect(result.current.isAdministrator).toBe(false)

    act(() => {
      setAuthGrants({ roles: ['administrator'], permissions: ['user:*:read'] })
    })
    expect(result.current.isAdministrator).toBe(true)
    expect(result.current.can('user:*:read')).toBe(true)

    act(() => {
      clearAuth()
    })
    expect(result.current.isAdministrator).toBe(false)
    expect(result.current.can('user:*:read')).toBe(false)
  })
})
