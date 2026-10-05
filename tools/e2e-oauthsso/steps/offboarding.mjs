import { getUser } from '../lib/admin.mjs'
import { check } from '../lib/check.mjs'
// Step 14 — the local offboarding: a holder whose provider refuses the
// refresh grant is banned, its sessions revoked, and the audit record
// written. The mock's token endpoint refuses unknown refresh tokens with
// `invalid_grant` (strict since mock-oauth2 4.0), and the ladder stages a
// dead refresh token the honest way — through the connection's own secret
// seal, read back from the database and copied onto the binding — then
// nudges the pending offboarding pass and watches the judgement land.
import { rpc } from '../lib/rpc.mjs'
import { sql } from '../lib/sql.mjs'
import { state } from '../lib/state.mjs'
import { walk } from '../lib/walk.mjs'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

export async function run() {
  console.log('14. The dead identity is offboarded (refresh grant refused)')
  {
    const flow = await walk('mock-custom', 'offboard-ernie')
    const answer = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', { flow_token: flow })
    const holderToken = answer.body.access_token ?? ''
    const holderRefresh = answer.body.refresh_token ?? ''
    check(
      '14a. the victim signs in',
      answer.body.status === 'success' && Boolean(holderToken),
      JSON.stringify(answer.body).slice(0, 200)
    )

    // Stage the dead refresh token: the connection's client secret is
    // re-sealed around a string the mock never minted, its sealed form
    // read from the database, copied onto the binding's refresh column,
    // and the connection's secret restored. The presented token is a
    // well-sealed bogus — exactly what the probe judges.
    const staged = await rpc(
      'saka.authn.v1.OAuthSSOService/UpdateConnection',
      {
        id: state.mapped.id,
        client_secret: 'bogus-refresh-token-the-mock-never-minted'
      },
      state.admin
    )
    check(
      '14b. the staged secret lands',
      staged.status === 200,
      JSON.stringify(staged.body).slice(0, 160)
    )
    sql(`UPDATE oauth_accounts SET refresh_token = (
      SELECT client_secret FROM oauth_connections WHERE provider = 'mock-custom')
      WHERE user_id = (SELECT id FROM users WHERE email = 'offboard-ernie@hogwarts.example')`)
    const restored = await rpc(
      'saka.authn.v1.OAuthSSOService/UpdateConnection',
      {
        id: state.mapped.id,
        client_secret: 'e2e-ladder-secret'
      },
      state.admin
    )
    check(
      '14c. the connection secret is restored',
      restored.status === 200,
      JSON.stringify(restored.body).slice(0, 160)
    )

    // The pending pass is nudged to now — the dispatcher's fallback poll
    // picks it up inside its minute, the way an hourly job waits.
    sql(`UPDATE queue_tasks SET wait_until = now() WHERE queue = 'oauth_offboarding'`)

    // The judgement: the account answers banned on the user read, its
    // binding stays (the ban, not the unlink, is the offboard), and the
    // audit record carries the provider's slug.
    let user = null
    for (let i = 0; i < 45; i++) {
      user = await getUser('offboard-ernie@hogwarts.example')
      if (user?.banned_at) break
      await sleep(2000)
    }
    check(
      '14d. the dead identity is banned',
      Boolean(user?.banned_at),
      JSON.stringify(user ?? {}).slice(0, 200)
    )
    const bindings = sql(`SELECT count(*) FROM oauth_accounts
      WHERE user_id = (SELECT id FROM users WHERE email = 'offboard-ernie@hogwarts.example')`)
    check('14e. the binding stays', bindings === '1', bindings)
    const auditRow = sql(`SELECT payload::text FROM audit_logs
      WHERE event = 'user_banned'
      AND user_id = (SELECT id FROM users WHERE email = 'offboard-ernie@hogwarts.example')`)
    check(
      '14f. the audit record names the provider',
      auditRow.includes('mock-custom'),
      auditRow.slice(0, 200)
    )

    // The revoked sessions: the access token a revoked session carries
    // keeps answering until it dies — the guard reads the signed claims,
    // not the row — so the revocation is judged where it bites, the
    // renewal: the holder's refresh token no longer renews anything.
    const dead = await rpc('saka.authn.v1.AuthService/Refresh', { refresh_token: holderRefresh })
    check(
      "14g. the holder's session no longer renews",
      dead.status === 401,
      JSON.stringify(dead.body).slice(0, 160)
    )
  }
}
