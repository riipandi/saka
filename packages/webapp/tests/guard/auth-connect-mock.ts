import { vi } from 'vite-plus/test'

/**
 * Transport-level Connect stubs for the auth-engine tests: the transport
 * speaks the Connect unary protocol (JSON body over POST to
 * /rpc/<service>/<method>), so the mocks answer that contract and nothing
 * about the engine's internals.
 */

/** Fixtures from Dan Brown. */
export const userJson = {
  id: 'user_v1_langdon',
  username: 'rlangdon',
  email: 'robert.langdon@example.com',
  displayName: 'Robert Langdon'
}

export const tokenJson = {
  accessToken: 'access-a',
  accessExpiresIn: 900,
  refreshToken: 'refresh-r',
  refreshExpiresIn: 86400,
  sessionId: 'sess_v1_zzz'
}

export const signInJson = { ...tokenJson, user: userJson, status: 'success' }

/** The `SessionService/GetSession` answer — the session the access token names. */
export const getSessionJson = {
  session: { id: tokenJson.sessionId, provider: 'password' },
  user: userJson,
  status: 'success',
  message: 'session restored'
}

export function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' }
  })
}

export function stringBody(body: BodyInit | null | undefined): string {
  if (typeof body === 'string') return body
  if (body instanceof Uint8Array) return new TextDecoder().decode(body)
  throw new Error('expected a JSON string request body')
}

export function stringUrl(request: RequestInfo | URL | undefined): string {
  if (typeof request !== 'string') throw new Error('expected a string request URL')
  return request
}

export function headerOf(call: Parameters<typeof fetch> | undefined, name: string): string | null {
  return new Headers(call?.[1]?.headers).get(name)
}

export function fetchStub() {
  return vi.fn<typeof fetch>()
}
