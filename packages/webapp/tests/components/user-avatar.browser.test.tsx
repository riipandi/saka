import { describe, expect, it, vi } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { UserAvatar } from '#/components/user-avatar'

describe('UserAvatar (browser)', () => {
  it('renders the deterministic blobatar when the account has no picture', async () => {
    const screen = await render(<UserAvatar name='hermione' />)
    // The fallback is the blob — an image whose source is the
    // deterministic SVG data URI the name seeds.
    let found = false
    await vi.waitFor(() => {
      found = screen.baseElement.querySelector('img') !== null
      expect(found).toBe(true)
    })
    const src = screen.baseElement.querySelector('img')?.getAttribute('src') ?? ''
    expect(src).toContain('data:image/svg')
  })

  it('renders the picture when the account has one', async () => {
    const picture =
      'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=='
    const screen = await render(<UserAvatar name='hermione' picture={picture} />)
    await expect.element(screen.getByRole('img', { name: 'hermione' })).toBeVisible()
  })
})
