import { test, expect } from '@playwright/test'
import { Secret, TOTP } from 'otpauth'
import {
  adminToken,
  bearer,
  closeSignInContexts,
  codeFromMail,
  createVerifiedUser,
  deleteUser,
  latestMail,
  type SeedUser
} from './helpers'

// The three token-bearing sign-in paths the sign-in completeness wave
// carried, driven on a real deployment: the multi-factor challenge (the
// authenticator's codes are computed from the enrollment's own secret, the
// way an app would), the password reset (the emailed code spent at the
// reset screen, the old credential dead afterwards), and one-time access
// (the emailed code typed into the login machine, the device token riding
// in the page's memory where the contract puts it).
//
// The passkeys are absent by design: the WebAuthn ceremony needs a virtual
// authenticator the CI suite does not manage, so they stay on the debug
// ladder (`passkey-ladder.test.ts`, `E2E_BUILD_TAG=debug`).

/** The six-digit value the authenticator would render right now. */
function totpCode(secretBase32: string): string {
  const totp = new TOTP({
    secret: Secret.fromBase32(secretBase32),
    algorithm: 'SHA1',
    digits: 6,
    period: 30
  })
  return totp.generate()
}

/**
 * The six-character one-time code the email carries: the standalone short
 * alphanumeric line. `codeFromMail` anchors twelve-character codes; the
 * one-time email path mints six, so this reads the shorter line.
 */
function oneTimeCodeFromMail(text: string): string | null {
  const lines = text
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => /^[A-Za-z0-9]{6}$/.test(line))
  return lines[lines.length - 1] ?? null
}

let admin: string
let mfaUser: SeedUser
let resetUser: SeedUser
let oneTimeUser: SeedUser

test.describe.serial('the sign-in completeness flows', () => {
  test.afterEach(async () => {
    await closeSignInContexts()
  })

  test.beforeAll(async ({ request }) => {
    admin = await adminToken(request)
    mfaUser = await createVerifiedUser(request, admin, `mfa${Date.now() % 100000}`)
    resetUser = await createVerifiedUser(request, admin, `rst${Date.now() % 100000}`)
    oneTimeUser = await createVerifiedUser(request, admin, `ota${Date.now() % 100000}`)
  })

  test.afterAll(async ({ request }) => {
    for (const user of [mfaUser, resetUser, oneTimeUser]) {
      if (user?.id) await deleteUser(request, admin, user.id)
    }
  })

  test('an MFA-enabled account signs in through the challenge', async ({ browser, request }) => {
    // The enrollment is wired before the browser act: a session the account
    // still holds (MFA is not yet on) earns the Bearer the enrollment asks.
    const signInResponse = await request.post('/rpc/saka.authn.v1.AuthService/SignIn', {
      data: { identity: mfaUser.username, password: mfaUser.password }
    })
    expect(signInResponse.status()).toBe(200)
    const userBearer = bearer((await signInResponse.json()).access_token)

    const begin = await request.post('/rpc/saka.authn.v1.MultifactorService/BeginTotpEnrollment', {
      headers: userBearer,
      data: { name: 'E2E authenticator' }
    })
    expect(begin.status()).toBe(200)
    const enrollment = await begin.json()

    const confirm = await request.post(
      '/rpc/saka.authn.v1.MultifactorService/ConfirmTotpEnrollment',
      {
        headers: userBearer,
        data: { totp_id: enrollment.totp_id, code: totpCode(enrollment.secret) }
      }
    )
    expect(confirm.status()).toBe(200)
    expect((await confirm.json()).recovery_codes.length).toBeGreaterThan(0)

    // The challenge is the next state of the same mount, not a new route —
    // the login form's own textboxes outlive the fork for a beat, so the
    // slots must be scoped to the challenge form or the first wait picks
    // the identity box the fork is about to replace.
    const context = await browser.newContext()
    const page = await context.newPage()
    await page.goto('/login')
    await page.fill('#identity', mfaUser.username)
    await page.fill('#password', mfaUser.password)
    await page.click('button[type=submit]')
    const code = page.locator('#mfa-challenge-form').getByRole('textbox')
    await expect(code).toHaveCount(6, { timeout: 15_000 })

    // A wrong factor reads as a refusal on the same screen.
    await code.first().pressSequentially('000000')
    await page.getByRole('button', { name: 'Verify' }).click()
    await expect(page.getByText('Verification failed')).toBeVisible({ timeout: 15_000 })

    // The authenticator's current code completes the sign-in. The first
    // wrong attempt leaves the slots full and the focus on the last box,
    // so the retype goes through a fresh visit to the fork.
    await page.goto('/login')
    await page.fill('#identity', mfaUser.username)
    await page.fill('#password', mfaUser.password)
    await page.click('button[type=submit]')
    await expect(code).toHaveCount(6, { timeout: 15_000 })
    await code.first().pressSequentially(totpCode(enrollment.secret))
    await page.getByRole('button', { name: 'Verify' }).click()
    await expect(page).toHaveURL(/\/overview$/, { timeout: 15_000 })
    await closeSignInContexts()
  })

  test('a reset code spends itself and kills the old credential', async ({ browser }) => {
    const context = await browser.newContext()
    const page = await context.newPage()
    await page.goto('/forgot-password')
    await page.getByLabel('Email').fill(resetUser.email)
    await page.getByRole('button', { name: 'Send reset code' }).click()
    await expect(page.getByText('Check your inbox')).toBeVisible({ timeout: 15_000 })

    const mail = await latestMail(/Reset your password/i, resetUser.email)
    expect(mail).not.toBeNull()
    const resetCode = codeFromMail(mail!.text)
    expect(resetCode).not.toBeNull()

    await page.goto(`/reset-password?token=${resetCode}`)
    await expect(page.getByLabel('Reset code')).toHaveValue(resetCode!, { timeout: 15_000 })
    await page.getByLabel('New password').fill('e2e-reset-password-2')
    await page.getByLabel('Confirm it').fill('e2e-reset-password-2')
    await page.getByRole('button', { name: 'Reset password' }).click()

    // The reset lands on the login route with its notice; terminate_sessions
    // rides its default, so no old session survives either.
    await expect(page.getByText('Password reset')).toBeVisible({ timeout: 15_000 })

    // The old credential is dead; the new one signs in.
    await page.fill('#identity', resetUser.username)
    await page.fill('#password', resetUser.password)
    await page.click('button[type=submit]')
    await expect(page.locator('#login-alert-error')).toBeVisible({ timeout: 15_000 })

    await page.fill('#password', 'e2e-reset-password-2')
    await page.click('button[type=submit]')
    await expect(page).toHaveURL(/\/overview$/, { timeout: 15_000 })
    await closeSignInContexts()
  })

  test('a one-time email code signs the account in', async ({ browser }) => {
    const context = await browser.newContext()
    const page = await context.newPage()
    await page.goto('/login')

    const entry = page.getByRole('button', { name: 'Email me a one-time sign-in code' })
    await expect(entry).toBeVisible({ timeout: 15_000 })
    await page.fill('#identity', oneTimeUser.email)
    await entry.click()

    const codeField = page.getByLabel('One-time code')
    await expect(codeField).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('Check your inbox')).toBeVisible()

    const mail = await latestMail(/Your login code/i, oneTimeUser.email)
    expect(mail).not.toBeNull()
    const code = oneTimeCodeFromMail(mail!.text)
    expect(code).not.toBeNull()

    await codeField.fill(code!)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page).toHaveURL(/\/overview$/, { timeout: 15_000 })
    await closeSignInContexts()
  })
})
