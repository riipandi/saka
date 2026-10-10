import { z } from 'zod'

/**
 * The grant snapshot the access token's payload carries — the mint-time
 * `roles`/`permissions` claims (`pkg/jwtutils` `AccessClaims`), read here so
 * the UI and the server judge the same wire facts.
 */
export interface AccessGrants {
  roles: string[]
  permissions: string[]
}

/** The empty snapshot every absent or unreadable answer normalizes to. */
export const emptyGrants = (): AccessGrants => ({ roles: [], permissions: [] })

const accessClaimsSchema = z.object({
  roles: z.array(z.string()).optional(),
  permissions: z.array(z.string()).optional()
})

/**
 * Read the grants out of an access token, answering the empty snapshot when
 * the token names none. The claims are the backend's own omission contract
 * (omitted from tokens that name neither), so absence is the normal shape —
 * only an unreadable payload warns, and even that must never throw: a bad
 * claim may not break the sign-in that just succeeded.
 */
export function decodeAccessClaims(accessToken: string | null | undefined): AccessGrants {
  if (!accessToken) return emptyGrants()

  const segments = accessToken.split('.')
  if (segments.length !== 3) {
    console.warn('auth-claims: access token is not a compact JWT')
    return emptyGrants()
  }

  let payload: unknown
  try {
    payload = JSON.parse(decodeBase64Url(segments[1] ?? ''))
  } catch (error) {
    console.warn('auth-claims: unreadable access token payload', error)
    return emptyGrants()
  }

  const parsed = accessClaimsSchema.safeParse(payload)
  if (!parsed.success) {
    console.warn('auth-claims: access token claims failed validation', parsed.error)
    return emptyGrants()
  }

  return {
    roles: parsed.data.roles ?? [],
    permissions: parsed.data.permissions ?? []
  }
}

function decodeBase64Url(segment: string): string {
  const base64 = segment.replace(/-/g, '+').replace(/_/g, '/')
  const padded = base64 + '='.repeat((4 - (base64.length % 4)) % 4)
  const bytes = Uint8Array.from(atob(padded), (char) => char.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}
