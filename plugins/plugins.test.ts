import { describe, expect, test } from 'vitest'
import devshellPlugin from './plugin-devshell.ts'
import emailPlugin from './plugin-email.ts'
import golangPlugin from './plugin-golang.ts'

describe('vite plugins', () => {
  test('email plugin exposes its name', () => {
    expect(emailPlugin().name).toBe('vite-plugin-email')
  })

  test('go plugin exposes its name', () => {
    expect(golangPlugin({ packageName: 'tango' }).name).toBe('vite-plugin-go')
  })

  test('devshell plugin exposes its name and only runs in serve', () => {
    const plugin = devshellPlugin()
    expect(plugin.name).toBe('vite-plugin-devshell')
    expect(plugin.apply).toBe('serve')
  })
})
