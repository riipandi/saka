import { test, expect } from '@playwright/test'
import {
  adminToken,
  bearer,
  codeFromMail,
  deleteUser,
  latestMail,
  signIn,
  typeCode,
  closeSignInContexts
} from './helpers'

// The email identity flow on a real deployment: the signup the wire seeds
// mails its verification code (an account cannot sign in before the proof),
// the wrong code reads as a failure on the same form, the real code lands
// through the no-session door, and the signed-in change flow moves the
// account to a second address by the code that address receives. Mailpit is
// the mailbox the codes are read from.
//
// The send-cooldown wording the plan named is not reachable through the UI:
// `SendEmail` answers only a signed-in caller, and the deployment refuses
// sign-in to an unverified account — the two facts meet so that no unverified
// caller ever holds a session. The wording stays covered by the page's
// browser test (the wire refusal mapped through `getErrorMessage`).

let admin: string
let userId: string
let username: string
let password: string
let firstEmail: string
let secondEmail: string

test.describe.serial('the account email flow', () => {
  test.afterEach(async () => {
    await closeSignInContexts()
  })

  test.beforeAll(async ({ request }) => {
    admin = await adminToken(request)

    // The invitation the signup runs under; the raw token exists once.
    const token = await request.post('/rpc/saka.identity.v1.SignupService/CreateSignupToken', {
      headers: bearer(admin),
      data: { ttlSeconds: 3600 }
    })
    expect(token.status()).toBe(200)
    const rawToken = (await token.json()).raw_token

    username = `e2e_mail${Date.now() % 100000}`
    password = 'e2e-password-1'
    firstEmail = `${username}@example.com`
    secondEmail = `${username}.moved@example.com`

    const signup = await request.post('/rpc/saka.identity.v1.SignupService/Signup', {
      data: {
        username,
        email: firstEmail,
        password,
        token: rawToken,
        firstName: 'E2E',
        lastName: 'Mailflow'
      }
    })
    expect(signup.status()).toBe(200)
    const body = await signup.json()
    expect(body.status).toBe('success')
    userId = body.user.id
  })

  test.afterAll(async ({ request }) => {
    if (userId) await deleteUser(request, admin, userId)
  })

  test('a wrong code reads as a failure, not a crash', async ({ page }) => {
    await page.goto('/verify-email?code=ZZZZZZZZZZZZ')
    await page.getByRole('button', { name: 'Verify' }).click()
    await expect(page.getByText(/invalid or expired/i)).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('Verify your email')).toBeVisible()
  })

  test('the mailed code verifies through the no-session door', async ({ page }) => {
    const mail = await latestMail(/verify/i, firstEmail)
    if (!mail) throw new Error('the signup verification mail never arrived')
    const code = codeFromMail(mail.text)
    if (!code) throw new Error('the verification mail carried no code')

    await page.goto(`/verify-email?code=${code}`)
    await page.getByRole('button', { name: 'Verify' }).click()
    await expect(page.getByText('Email verified')).toBeVisible({ timeout: 15_000 })
  })

  test('the account signs in and the page reads the verified state', async ({ browser }) => {
    const page = await signIn(browser, { username, password })

    await page.goto('/account/email')
    await expect(page.getByText('Verified')).toBeVisible()
    await expect(page.getByText(firstEmail)).toBeVisible()
    await expect(page.getByText('Verify this address')).toHaveCount(0)
  })

  test("the change flow moves the account by the new address's code", async ({ browser }) => {
    const page = await signIn(browser, { username, password })
    await page.goto('/account/email')

    await page.locator('#new-email').fill(secondEmail)
    await page.getByRole('button', { name: 'Request change' }).click()
    await expect(page.getByText(`The confirm code was sent to ${secondEmail}`)).toBeVisible({
      timeout: 15_000
    })

    // The code went to the address the change moves to, not the old one;
    // its subject asks the reader to confirm, not to change.
    const mail = await latestMail(/confirm/i, secondEmail)
    if (!mail) throw new Error('the change mail never arrived')
    const code = codeFromMail(mail.text)
    if (!code) throw new Error('the change mail carried no code')

    await typeCode(page, code)
    await page.getByRole('button', { name: 'Confirm change' }).click()
    await expect(page.getByText('The email address was changed.')).toBeVisible({
      timeout: 15_000
    })

    // A reload answers from the server: the address is the new one and the
    // proof re-stamped with it.
    await page.reload()
    await expect(page.getByText(secondEmail)).toBeVisible()
    await expect(page.getByText('Verified')).toBeVisible()
  })
})
