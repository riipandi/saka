// Steps 1–5 — the sign-in shape the first plan shipped: the JIT with its
// names pause, the binding's straight session, the verified-address link,
// the unverified gate spending its email code, and the continue step a
// nameless provider waits for.
import { rpc } from '../lib/rpc.mjs'
import { walk } from '../lib/walk.mjs'
import { mailpitClear, mailpitWait } from '../lib/mailpit.mjs'
import { check } from '../lib/check.mjs'
import { state } from '../lib/state.mjs'

export async function run() {
  console.log('1. JIT sign-in, open mode, the names step (github sim)')
  {
    const flow = await walk('mock-github', 'alice')
    const paused = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', { flow_token: flow })
    check('1a. the names pause answers', paused.body.stage === 'require_names', JSON.stringify(paused.body))
    const done = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', {
      flow_token: flow, given_name: 'Alice', family_name: 'Hogwarts',
    })
    state.aliceToken = done.body.access_token ?? ''
    state.aliceId = done.body.user?.id ?? ''
    check('1b. the JIT account opens a session',
      done.body.status === 'success' && Boolean(state.aliceToken),
      String(done.body.message ?? JSON.stringify(done.body)).slice(0, 160))
    check('1c. the derived username and display name', done.body.user?.display_name === 'Alice Hogwarts',
      JSON.stringify(done.body.user ?? {}).slice(0, 160))
  }

  console.log('2. The binding signs the same provider identity in without the names step')
  {
    const flow = await walk('mock-github', 'alice')
    const answer = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', { flow_token: flow })
    check('2a. the binding branch answers a session',
      answer.body.status === 'success' && answer.body.user?.id === state.aliceId,
      JSON.stringify(answer.body).slice(0, 160))
  }

  console.log('3. Linking on a verified email (google sim, account pre-exists)')
  {
    const created = await rpc('saka.identity.v1.UserService/CreateUser', {
      username: 'sophie',
      email: 'sophie@hogwarts.example',
      password: 'Vetra#CERN-4782x9',
      first_name: 'Sophie',
      last_name: 'Hogwarts',
      email_verified: true,
    }, state.admin)
    check('3a. the account the address names exists', created.status === 200, JSON.stringify(created.body).slice(0, 160))
    const flow = await walk('mock-google', 'sophie')
    const answer = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', { flow_token: flow })
    check('3b. the verified address links and signs in', answer.body.status === 'success',
      JSON.stringify(answer.body).slice(0, 160))
  }

  console.log('4. The unverified email gate and the code (google sim)')
  {
    await mailpitClear()
    const flow = await walk('mock-google', 'unverified-bob')
    const paused = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', { flow_token: flow })
    check('4a. the flow pauses at verify_email', paused.body.stage === 'verify_email', JSON.stringify(paused.body))
    const code = await mailpitWait('unverified-bob@hogwarts.example')
    check('4b. the code arrived', Boolean(code), 'no mailpit message')
    const wrong = await rpc('saka.authn.v1.OAuthSSOService/VerifySignInEmail', { flow_token: flow, code: 'WRONGCODE12' })
    check('4c. a wrong code answers invalid_argument', wrong.status === 400, JSON.stringify(wrong.body).slice(0, 120))
    const right = await rpc('saka.authn.v1.OAuthSSOService/VerifySignInEmail',
      { flow_token: flow, code: code ?? 'AAAAAAAA12' })
    check('4d. the right code moves the flow on', right.body.stage === 'resolved', JSON.stringify(right.body))
    const done = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', {
      flow_token: flow, given_name: 'Bob', family_name: 'Hogwarts',
    })
    check('4e. the proven address JIT-creates the account', done.body.status === 'success',
      JSON.stringify(done.body).slice(0, 160))
  }

  console.log('5. The continue step for a provider that named nobody (google sim)')
  {
    const flow = await walk('mock-google', 'nonames-carl')
    const paused = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', { flow_token: flow })
    check('5a. the names pause', paused.body.stage === 'require_names', JSON.stringify(paused.body))
    const done = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', {
      flow_token: flow, given_name: 'Carl', family_name: 'Hogwarts',
    })
    check('5b. the collected names complete the sign-in', done.body.status === 'success',
      JSON.stringify(done.body).slice(0, 160))
  }
}
