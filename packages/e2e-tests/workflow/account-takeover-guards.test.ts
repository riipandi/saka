import { test, expect } from '@playwright/test'
import {
  adminToken,
  bearer,
  createVerifiedUser,
  deleteUser,
  isRecord,
  type SeedUser
} from './helpers'

// The two procedures that would let a stolen access token take an account
// for good — moving its address and deleting it — answer only a session
// carrying the holder's single-use step-up proof.

let admin: string
let account: SeedUser | undefined

test.describe.serial('the account takeover guards', () => {
  test.beforeAll(async ({ request }) => {
    admin = await adminToken(request)
    account = await createVerifiedUser(request, admin, `guard${Date.now() % 100000}`)
  })

  test.afterAll(async ({ request }) => {
    if (account) await deleteUser(request, admin, account.id)
  })

  test('a token without a proof is refused, and a proof is spent exactly once', async ({
    request
  }) => {
    if (!account) throw new Error('the beforeAll seed never produced the account')
    const signedIn = await request.post('/rpc/saka.authn.v1.AuthService/SignIn', {
      data: { identity: account.username, password: account.password }
    })
    expect(signedIn.status()).toBe(200)
    const signedInBody: unknown = await signedIn.json()
    const token = isRecord(signedInBody) ? signedInBody.access_token : undefined
    if (typeof token !== 'string') throw new Error('the sign-in answered no access token')

    const change = (headers: Record<string, string>) =>
      request.post('/rpc/saka.identity.v1.EmailVerificationService/RequestEmailChange', {
        headers: { ...bearer(token), ...headers },
        data: { newEmail: `moved.${account?.email}` }
      })
    const remove = () =>
      request.post('/rpc/saka.identity.v1.UserService/DeleteMyAccount', {
        headers: bearer(token),
        data: {}
      })

    expect((await change({})).status()).toBe(401)
    expect((await remove()).status()).toBe(401)

    const proved = await request.post('/rpc/saka.authn.v1.WebAuthnService/Reauthenticate', {
      headers: bearer(token),
      data: { password: account.password }
    })
    expect(proved.status()).toBe(200)
    const provedBody: unknown = await proved.json()
    const proof = isRecord(provedBody) ? provedBody.token : undefined
    if (typeof proof !== 'string') throw new Error('the reauthentication answered no proof')

    expect((await change({ 'x-saka-reauthentication': proof })).status()).toBe(200)
    expect((await change({ 'x-saka-reauthentication': proof })).status()).toBe(401)

    // The refused deletion left the account where it was.
    const stillThere = await request.post('/rpc/saka.authn.v1.AuthService/SignIn', {
      data: { identity: account.username, password: account.password }
    })
    expect(stillThere.status()).toBe(200)
  })
})
