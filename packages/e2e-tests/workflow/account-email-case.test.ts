import { test, expect } from '@playwright/test'
import { adminToken, bearer, createVerifiedUser, deleteUser, type SeedUser } from './helpers'

// An address is one account whatever its case: the sign-in identity folds
// it, and a second account under the same address in another case is
// refused where the first was created.

let admin: string
let account: SeedUser | undefined

test.describe.serial('the email case flow', () => {
  test.beforeAll(async ({ request }) => {
    admin = await adminToken(request)
    account = await createVerifiedUser(request, admin, `case${Date.now() % 100000}`)
  })

  test.afterAll(async ({ request }) => {
    if (account) await deleteUser(request, admin, account.id)
  })

  test('signs in by the address in another case', async ({ request }) => {
    if (!account) throw new Error('the beforeAll seed never produced the account')
    const signedIn = await request.post('/rpc/saka.authn.v1.AuthService/SignIn', {
      data: { identity: account.email.toUpperCase(), password: account.password }
    })
    expect(signedIn.status()).toBe(200)
  })

  test('refuses a second account under the same address in another case', async ({ request }) => {
    if (!account) throw new Error('the beforeAll seed never produced the account')
    const duplicate = await request.post('/rpc/saka.identity.v1.UserService/CreateUser', {
      headers: bearer(admin),
      data: {
        username: `${account.username}_twin`,
        email: account.email.toUpperCase(),
        password: account.password,
        firstName: 'E2E',
        lastName: 'Twin',
        emailVerified: true
      }
    })
    expect(duplicate.status()).toBeGreaterThanOrEqual(400)
  })
})
