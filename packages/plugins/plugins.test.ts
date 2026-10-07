import { describe, expect, test } from 'vitest'
import golangPlugin from './plugin-golang.ts'

describe('vite plugins', () => {
  test('go plugin exposes its name', () => {
    expect(golangPlugin({ packageName: 'saka' }).name).toBe('vite-plugin-go')
  })
})
