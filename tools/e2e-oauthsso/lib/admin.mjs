// The admin's door onto the rows the ladder drives: the custom
// connections it walks through (created when absent, deleted by the
// cleanup step) and the account reads its probes judge.
import { rpc } from './rpc.mjs'
import { state } from './state.mjs'

// The mapping the custom sim rides: every field names a nonstandard
// claim the mock answers — user_uuid, mail, mail_verified, handle, photo.
export const customMapping = {
  subject: 'user_uuid',
  email: 'mail',
  email_verified: 'mail_verified',
  email_verified_default: true,
  username: 'handle',
  avatar_url: 'photo',
}

export async function ensureConnection(provider, issuer, mapping, attrs) {
  const { body } = await rpc('saka.authn.v1.OAuthSSOService/ListConnections', {}, state.admin)
  const existing = (body.connections ?? []).find((c) => c.provider === provider)
  if (existing) return existing
  const request = {
    kind: 'custom',
    provider,
    display_name: `E2E ${issuer}`,
    discovery_url: `https://localhost:3220/${issuer}/.well-known/openid-configuration`,
    client_id: 'e2e-ladder',
    client_secret: 'e2e-ladder-secret',
    enabled: true,
  }
  if (mapping) request.attribute_mapping = mapping
  if (attrs) request.custom_attributes = attrs
  const created = await rpc('saka.authn.v1.OAuthSSOService/CreateConnection', request, state.admin)
  if (created.status !== 200) throw new Error(`connection ${provider} refused: ${JSON.stringify(created.body).slice(0, 200)}`)
  return created.body.connection
}

export async function getUser(email) {
  const { body } = await rpc('saka.identity.v1.UserService/ListUsers', { search: email }, state.admin)
  return (body.users ?? []).find((u) => u.email === email) ?? null
}

export async function deleteConnections() {
  const conns = await rpc('saka.authn.v1.OAuthSSOService/ListConnections', {}, state.admin)
  for (const conn of conns.body.connections ?? []) {
    if ((conn.provider ?? '').startsWith('mock-')) {
      await rpc('saka.authn.v1.OAuthSSOService/DeleteConnection', { id: conn.id }, state.admin)
    }
  }
}
