import { describe, expect, test } from 'vitest'
import emailPlugin from './plugin-email.ts'
import golangPlugin from './plugin-golang.ts'

describe('vite plugins', () => {
  test('email plugin exposes its name', () => {
    expect(emailPlugin().name).toBe('vite-plugin-email')
  })

  test('go plugin exposes its name', () => {
    expect(golangPlugin({ packageName: 'tango' }).name).toBe('vite-plugin-go')
  })
})
