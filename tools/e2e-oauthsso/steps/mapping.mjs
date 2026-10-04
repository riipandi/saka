// Steps 9–12 — the extended attribute mapping this round shipped: a
// mapped subject binding the account, the verified default steering the
// unverified gate, the every-sign-in refresh of the names and the custom
// attributes with the username holding still, and the picture the mapping
// names landing on the account.
import { rpc } from '../lib/rpc.mjs'
import { walk } from '../lib/walk.mjs'
import { sql } from '../lib/sql.mjs'
import { check } from '../lib/check.mjs'
import { customMapping, getUser } from '../lib/admin.mjs'
import { state } from '../lib/state.mjs'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

export async function run() {
  console.log('9. The extended mapping rides a custom connection (custom sim)')
  // The mock answers nonstandard claim names — user_uuid, mail, mail_verified,
  // handle, photo, dept — and the connection maps each one explicitly.
  {
    const flow = await walk('mock-custom', 'mapped-padma')
    const answer = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', { flow_token: flow })
    state.padmaId = answer.body.user?.id ?? ''
    check('9a. the mapped subject JIT-creates the account',
      answer.body.status === 'success' && Boolean(state.padmaId), JSON.stringify(answer.body).slice(0, 200))
    check('9b. the mapped username is the account\'s handle',
      answer.body.user?.username === 'hh_mappedpadma',
      JSON.stringify(answer.body.user ?? {}).slice(0, 160))
  }

  console.log('10. The verified default steers the unverified gate (custom sim)')
  // noverify-* answers no mail_verified claim at all; the mapping's
  // email_verified_default=true is what lets the sign-in through.
  {
    const flow = await walk('mock-custom', 'noverify-quinn')
    const answer = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', { flow_token: flow })
    check('10a. no verified claim, the default says proven — no pause',
      answer.body.status === 'success', JSON.stringify(answer.body).slice(0, 200))
  }

  console.log('11. The second sign-in refreshes the mapping\'s answers (custom sim)')
  // The connection's mapping moves given_name onto the second_given claim and
  // the custom attribute onto the house claim; the same provider identity —
  // the mapped user_uuid — signs in again and the account takes the new
  // answers, while its username stays what the JIT creation wrote.
  {
    const rewrite = await rpc('saka.authn.v1.OAuthSSOService/UpdateConnection', {
      id: state.mapped.id,
      attribute_mapping: { ...customMapping, given_name: 'second_given' },
      custom_attributes: [{ key: 'department', claim: 'house' }],
    }, state.admin)
    check('11a. the mapping rewrite lands', rewrite.status === 200, JSON.stringify(rewrite.body).slice(0, 160))
    const flow = await walk('mock-custom', 'mapped-padma')
    const answer = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', { flow_token: flow })
    check('11b. the refresh binds the same account',
      answer.body.status === 'success' && answer.body.user?.id === state.padmaId,
      JSON.stringify(answer.body).slice(0, 200))
    // The token's user view is the mint's snapshot — the profile it names
    // can trail the row by the sign-in that refreshed it. The account read
    // is the fresh judgement.
    const fresh = await getUser('mapped-padma@hogwarts.example')
    check('11c. the names refreshed under the moved mapping',
      fresh?.first_name === 'Minerva',
      JSON.stringify(fresh ?? {}).slice(0, 200))
    check('11d. the username survived the refresh',
      fresh?.username === 'hh_mappedpadma',
      JSON.stringify(fresh ?? {}).slice(0, 160))
    const attrs = sql("SELECT custom_attributes FROM users WHERE email = 'mapped-padma@hogwarts.example'")
    check('11e. the custom attributes merged', attrs.includes('"department"') && attrs.includes('hufflepuff'), attrs.slice(0, 160))
  }

  console.log('12. The picture the mapping names lands on the account')
  // The picture pipeline is the queue's own; the read answers the object's
  // URL once the upload syncs, so the probe polls.
  {
    let picture = ''
    for (let i = 0; i < 20; i++) {
      picture = (await getUser('mapped-padma@hogwarts.example'))?.picture ?? ''
      if (picture) break
      await sleep(1000)
    }
    check('12a. the picture is stored and answered', Boolean(picture), picture.slice(0, 160))
  }
}
