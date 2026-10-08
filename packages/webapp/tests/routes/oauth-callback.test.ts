import { describe, expect, it } from 'vite-plus/test'
import { OAUTH_FLOW_ERROR_MESSAGES } from '#/routes/(auth)/oauth-callback'

describe('the OAuth flow error codes', () => {
  it('names every code the backend redirects carry', () => {
    expect(Object.keys(OAUTH_FLOW_ERROR_MESSAGES).toSorted()).toEqual([
      'internal_error',
      'provider_error',
      'unknown_flow'
    ])
  })

  it('falls back for an unknown code', () => {
    expect(OAUTH_FLOW_ERROR_MESSAGES['something-else']).toBeUndefined()
  })
})
