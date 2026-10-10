import { describe, expect, it, vi } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { clearAuth, setAuthGrants } from '#/libraries/guard/auth-store'
import { Can } from '#/libraries/guard/can'

/** The gate's absence check: the text is nowhere in the rendered document. */
function gone(screen: { baseElement: HTMLElement }, text: string): boolean {
  return !screen.baseElement.textContent?.includes(text)
}

describe('the Can render gate (browser)', () => {
  it('renders the children the snapshot admits', async () => {
    setAuthGrants({ roles: ['viewer'], permissions: ['user:*:read'] })

    const screen = await render(
      <Can permission='user:usr_9:read'>
        <span>the guarded pane</span>
      </Can>
    )
    await expect.element(screen.getByText('the guarded pane')).toBeVisible()

    clearAuth()
  })

  it('renders nothing, or the fallback, when the requirement is unmet', async () => {
    setAuthGrants({ roles: [], permissions: [] })

    const screen = await render(
      <>
        <Can permission='user:*:ban'>
          <span>a pane never shown</span>
        </Can>
        <Can permission='user:*:ban' fallback={<span>not allowed</span>}>
          <span>its shadow</span>
        </Can>
      </>
    )

    expect(gone(screen, 'a pane never shown')).toBe(true)
    expect(gone(screen, 'its shadow')).toBe(true)
    await expect.element(screen.getByText('not allowed')).toBeVisible()

    clearAuth()
  })

  it('re-renders when custody replaces the snapshot', async () => {
    setAuthGrants({ roles: [], permissions: [] })

    const screen = await render(
      <Can permission='user:*:ban' fallback={<span>sealed</span>}>
        <span>opened</span>
      </Can>
    )
    await expect.element(screen.getByText('sealed')).toBeVisible()

    setAuthGrants({ roles: ['administrator'], permissions: ['user:*:ban'] })
    await expect.element(screen.getByText('opened')).toBeVisible()
    await vi.waitFor(() => {
      expect(gone(screen, 'sealed')).toBe(true)
    })

    clearAuth()
    await expect.element(screen.getByText('sealed')).toBeVisible()
  })

  it('names a malformed slug at the render instead of hiding silently', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    const screen = await render(
      <Can permission='user:list'>
        <span>two-segment pane</span>
      </Can>
    )

    expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining('not a resource:instance:action'))
    expect(gone(screen, 'two-segment pane')).toBe(true)
    warnSpy.mockRestore()

    clearAuth()
  })
})
