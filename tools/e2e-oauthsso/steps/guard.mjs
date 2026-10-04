import { check } from '../lib/check.mjs'
import { mailpitClear, mailpitWait } from '../lib/mailpit.mjs'
// Steps 6–8 — the guards around the sign-in: the stranding refusal behind
// the step-up gate, the MFA bridge a protected account waits behind, and
// the invite mode that closes the JIT door.
import { rpc } from '../lib/rpc.mjs'
import { state } from '../lib/state.mjs'
import { totp } from '../lib/totp.mjs'
import { walk } from '../lib/walk.mjs'

export async function run() {
  console.log('6. The stranding refusal behind the step-up gate (alice: no password)')
  {
    const linked = await rpc(
      'saka.authn.v1.OAuthSSOService/ListLinkedConnections',
      {},
      state.aliceToken
    )
    check(
      '6a. the ledger answers the binding',
      (linked.body.linked_accounts ?? []).length === 1,
      JSON.stringify(linked.body).slice(0, 160)
    )
    const bindingId = linked.body.linked_accounts[0].id
    const unproven = await rpc(
      'saka.authn.v1.OAuthSSOService/UnlinkConnection',
      { linked_account_id: bindingId },
      state.aliceToken
    )
    check(
      '6b. the unproven caller is refused first',
      unproven.status === 401,
      `${unproven.status} ${JSON.stringify(unproven.body).slice(0, 120)}`
    )
    await mailpitClear()
    await rpc('saka.authn.v1.WebAuthnService/SendReauthenticationCode', {}, state.aliceToken)
    const code = await mailpitWait('alice@hogwarts.example')
    check('6c. the reverification code arrived', Boolean(code), 'no mailpit message')
    const proof = await rpc(
      'saka.authn.v1.WebAuthnService/Reauthenticate',
      { email_code: code ?? 'AAAAAAAA12' },
      state.aliceToken
    )
    check(
      '6d. the proof is answered once',
      Boolean(proof.body.token),
      JSON.stringify(proof.body).slice(0, 120)
    )
    const refused = await rpc(
      'saka.authn.v1.OAuthSSOService/UnlinkConnection',
      { linked_account_id: bindingId },
      state.aliceToken,
      proof.body.token
    )
    check(
      '6e. the last credential is refused',
      refused.status === 400 && (refused.body.message ?? '').includes('password'),
      `${refused.status} ${JSON.stringify(refused.body).slice(0, 120)}`
    )
  }

  console.log('7. The MFA bridge after OAuth (alice)')
  {
    const begun = await rpc(
      'saka.authn.v1.MultifactorService/BeginTotpEnrollment',
      { name: 'Tower' },
      state.aliceToken
    )
    const { secret = '', totp_id = '' } = begun.body
    check(
      '7a. the enrollment starts',
      Boolean(secret) && Boolean(totp_id),
      JSON.stringify(begun.body).slice(0, 120)
    )
    const confirmed = await rpc(
      'saka.authn.v1.MultifactorService/ConfirmTotpEnrollment',
      { totp_id, code: totp(secret) },
      state.aliceToken
    )
    check(
      '7b. the factor confirms',
      confirmed.status === 200,
      JSON.stringify(confirmed.body).slice(0, 120)
    )
    const flow = await walk('mock-github', 'alice')
    const fork = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', { flow_token: flow })
    check(
      '7c. the fork answers the bridge, never a session',
      fork.body.mfa_required === true && Boolean(fork.body.mfa_pending_token),
      JSON.stringify(fork.body).slice(0, 160)
    )
    const opened = await rpc(
      'saka.authn.v1.MultifactorService/CompleteSignIn',
      { pending_token: fork.body.mfa_pending_token, code: totp(secret) },
      state.aliceToken
    )
    check(
      '7d. the second factor opens the session',
      opened.body.status === 'success',
      JSON.stringify(opened.body).slice(0, 160)
    )
  }

  console.log('8. Invite mode closes the JIT door')
  {
    const moved = await rpc(
      'saka.settings.v1.SettingsService/Update',
      { key: 'access.mode', value: 'invite' },
      state.admin
    )
    check('8a. the setting moved', moved.status === 200, JSON.stringify(moved.body).slice(0, 120))
    const flow = await walk('mock-google', 'mallory')
    const answer = await rpc('saka.authn.v1.OAuthSSOService/ContinueSignIn', { flow_token: flow })
    check(
      '8b. the JIT creation is refused',
      answer.status === 403,
      `${answer.status} ${JSON.stringify(answer.body).slice(0, 120)}`
    )
    await rpc(
      'saka.settings.v1.SettingsService/Update',
      { key: 'access.mode', value: 'open' },
      state.admin
    )
  }
}
