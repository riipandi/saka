import { test, expect } from '@playwright/test'
import { Secret, TOTP } from 'otpauth'
import {
  adminToken,
  bearer,
  createVerifiedUser,
  deleteUser,
  isRecord,
  type SeedUser
} from './helpers'

// A wrong second-factor code counts against the account, not only the bridge
// it was typed into: a fresh bridge costs one correct password, so guesses
// spread across bridges still add up to the lockout — and a locked account
// answers the same refusal whether the code is right or wrong.

function totpCode(secretBase32: string): string {
  return new TOTP({
    secret: Secret.fromBase32(secretBase32),
    algorithm: 'SHA1',
    digits: 6,
    period: 30
  }).generate()
}

let admin: string
let account: SeedUser | undefined

test.describe.serial('the second factor and the lockout', () => {
  test.beforeAll(async ({ request }) => {
    admin = await adminToken(request)
    account = await createVerifiedUser(request, admin, `lock${Date.now() % 100000}`)
  })

  test.afterAll(async ({ request }) => {
    if (account) await deleteUser(request, admin, account.id)
  })

  test('wrong codes across fresh bridges lock the account', async ({ request }) => {
    if (!account) throw new Error('the beforeAll seed never produced the account')
    const seeded = account

    const signIn = () =>
      request.post('/rpc/saka.authn.v1.AuthService/SignIn', {
        data: { identity: seeded.username, password: seeded.password }
      })
    const field = async (response: Awaited<ReturnType<typeof signIn>>, key: string) => {
      const body: unknown = await response.json()
      const value = isRecord(body) ? body[key] : undefined
      if (typeof value !== 'string') throw new Error(`the answer carries no ${key}`)
      return value
    }

    // Enrol an authenticator while the account still signs in with one factor.
    const first = await signIn()
    expect(first.status()).toBe(200)
    const userBearer = bearer(await field(first, 'access_token'))
    const begin = await request.post('/rpc/saka.authn.v1.MultifactorService/BeginTotpEnrollment', {
      headers: userBearer,
      data: { name: 'E2E authenticator' }
    })
    const enrollment: unknown = await begin.json()
    if (!isRecord(enrollment)) throw new Error('the enrollment answer is not an object')
    const secret = String(enrollment.secret)
    const confirm = await request.post(
      '/rpc/saka.authn.v1.MultifactorService/ConfirmTotpEnrollment',
      { headers: userBearer, data: { totp_id: enrollment.totp_id, code: totpCode(secret) } }
    )
    expect(confirm.status()).toBe(200)

    // The lockout bound is ten failures: each bridge allows three guesses, so
    // four bridges of wrong codes cross it. A bridge that was minted still
    // answers its refusal until the account locks.
    let wrong = 0
    while (wrong < 10) {
      const bridge = await signIn()
      if (bridge.status() !== 200) break
      const pending = await field(bridge, 'mfa_pending_token')
      for (let guess = 0; guess < 3 && wrong < 10; guess += 1) {
        const refused = await request.post('/rpc/saka.authn.v1.MultifactorService/CompleteSignIn', {
          data: { pending_token: pending, code: '000000' }
        })
        expect(refused.status()).toBe(401)
        wrong += 1
      }
    }

    // The account is locked: the right password and the right code are both
    // refused, and the refusal is the generic credential one — the lockout is
    // not an oracle.
    const locked = await signIn()
    expect(locked.status()).toBe(401)

    // The administrator's unlock opens it again.
    const unlocked = await request.post('/rpc/saka.identity.v1.UserService/UnlockUser', {
      headers: bearer(admin),
      data: { id: seeded.id }
    })
    expect(unlocked.status()).toBe(200)
    const again = await signIn()
    expect(again.status()).toBe(200)
  })
})
