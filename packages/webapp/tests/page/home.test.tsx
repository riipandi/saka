import { describe, expect, it } from 'vite-plus/test'
import { renderPage } from '../helpers'

describe('Homepage', () => {
  it('should render successfully', async () => {
    const { baseElement } = await renderPage('/')
    expect(baseElement).toBeTruthy()
  })
})
