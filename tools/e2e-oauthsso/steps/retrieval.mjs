// Step 13 — the token retrieval this round shipped: the holder signs in
// over the custom sim, lists their own binding, and reads the provider
// tokens over the wire — opened from their seal for the answer, with the
// expiry the provider named. A foreign read answers not_found; the stored
// columns stay sealed.
import { rpc } from '../lib/rpc.mjs'
import { walk } from '../lib/walk.mjs'
import { sql } from '../lib/sql.mjs'
import { check } from '../lib/check.mjs'
import { state } from '../lib/state.mjs'

export async function run() {
  console.log('13. The holder reads their provider tokens (custom sim)')
  {
    const flow = await walk('mock-custom', 'tokens-ernie')
    const answer = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', { flow_token: flow })
    state.holderToken = answer.body.access_token ?? ''
    check('13a. the holder signs in',
      answer.body.status === 'success' && Boolean(state.holderToken),
      JSON.stringify(answer.body).slice(0, 200))

    const linked = await rpc('saka.authn.v1.OAuthSSOService/ListLinkedConnections', {}, state.holderToken)
    const binding = (linked.body.linked_accounts ?? []).find((l) => l.provider === 'mock-custom')
    state.holderBinding = binding?.id ?? ''
    check('13b. the binding is listed to its holder',
      Boolean(state.holderBinding), JSON.stringify(linked.body).slice(0, 200))

    const tokens = await rpc('saka.authn.v1.OAuthSSOService/GetLinkedAccountTokens',
      { linked_account_id: state.holderBinding }, state.holderToken)
    check('13c. the access token answers opened',
      Boolean(tokens.body.access_token), JSON.stringify(tokens.body).slice(0, 200))
    check('13d. the refresh token answers opened',
      Boolean(tokens.body.refresh_token), JSON.stringify(tokens.body).slice(0, 200))
    check('13e. the access expiry answers',
      Boolean(tokens.body.expires_at), JSON.stringify(tokens.body).slice(0, 200))
    check('13f. the answer names the connection',
      tokens.body.provider === 'mock-custom', JSON.stringify(tokens.body).slice(0, 120))

    // A read by another session is the same not_found a missing binding
    // answers — the tokens belong to the holder alone.
    const foreign = await rpc('saka.authn.v1.OAuthSSOService/GetLinkedAccountTokens',
      { linked_account_id: state.holderBinding }, state.admin)
    check('13g. a foreign read answers not_found',
      foreign.status === 404, JSON.stringify(foreign.body).slice(0, 200))

    // The columns rest sealed: the row's access token is the `enc:` form,
    // and the opened answer appears nowhere in it.
    const sealed = sql(`SELECT access_token FROM oauth_linked_accounts WHERE id = (
      SELECT l.id FROM oauth_linked_accounts l
      JOIN users u ON u.id = l.user_id
      WHERE u.email = 'tokens-ernie@hogwarts.example')`)
    check('13h. the tokens rest sealed on the row',
      sealed.startsWith('enc:') && !sealed.includes(tokens.body.access_token),
      sealed.slice(0, 80))
  }
}
