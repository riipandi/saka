import { test, expect } from '@playwright/test'
import {
  adminToken,
  createVerifiedUser,
  deleteUser,
  PNG_BYTES,
  signIn,
  updateSetting,
  type SeedUser,
  closeSignInContexts
} from './helpers'

// The profile flow on a real deployment: the throwaway account signs in,
// edits its names, reloads, sees them persisted, uploads a picture and
// resets it, and — with the deployment's gate opened for the act — deletes
// itself through the typed confirmation. The wire seeds the account, the
// act cleans up after itself.

let admin: string
let user: SeedUser | undefined

/** The seed the serial describe ran first; a missing one is a run bug. */
function requireUser(): SeedUser {
  if (!user) throw new Error('the beforeAll seed never produced the account')
  return user
}

test.describe.serial('the account profile flow', () => {
  test.afterEach(async () => {
    await closeSignInContexts()
  })

  test.beforeAll(async ({ request }) => {
    admin = await adminToken(request)
    user = await createVerifiedUser(request, admin, `prof${Date.now() % 100000}`)
  })

  test.afterAll(async ({ request }) => {
    // The delete test consumed its own account (it clears the handle);
    // anything left — an earlier failure's remains — still leaves here.
    if (user) await deleteUser(request, admin, user.id)
  })

  test('signs the throwaway in through the form', async ({ browser }) => {
    await signIn(browser, requireUser())
  })

  test('saves the profile and reads it back after a reload', async ({ browser }) => {
    const account = requireUser()
    const page = await signIn(browser, requireUser())
    await page.goto('/settings')
    await expect(page).toHaveURL(/\/settings$/)

    const display = page.locator('#display-name')
    await expect(display).toHaveValue(/Throwaway/)
    const saveBtn = page.getByRole('button', { name: 'Save changes' })
    await display.fill('E2E Profile Flow')
    // The fill's re-render must commit before the click — a click landing on
    // the not-yet-dirty (disabled) button is swallowed by the browser.
    await expect(saveBtn).toBeEnabled()
    await saveBtn.click()
    await expect(saveBtn).toBeEnabled()

    // The answered view is the authority: the reload reads the server, and
    // the server holds the name the save wrote. (The store patch the save
    // also performs is the browser tests' assertion.)
    await page.reload()
    await expect(page.locator('#display-name')).toHaveValue('E2E Profile Flow')
    expect(account.email).toContain('@example.com')
  })

  test('uploads a picture and resets it', async ({ browser }) => {
    const page = await signIn(browser, requireUser())
    await page.goto('/settings')

    await page.setInputFiles('input[type=file]', {
      name: 'picture.png',
      mimeType: 'image/png',
      buffer: PNG_BYTES
    })

    // The upload answers through the avatar: the picture URL is the
    // storage path the wire carries, cache-busted by the view's epoch.
    await expect(page.locator('img[src*="/storage/"]').first()).toBeVisible()

    await page.getByRole('button', { name: 'Reset', exact: true }).click()
    // The reset clears the wire's picture: the avatar falls back to the
    // seeded blob the component renders when the account carries none.
    await expect(page.locator('img[src*="/storage/"]')).toHaveCount(0)
  })

  test('the danger zone deletes the account when the gate is opened', async ({
    request,
    browser
  }) => {
    const account = requireUser()
    const page = await signIn(browser, requireUser())
    await updateSetting(request, admin, 'users.self_delete_enabled', 'true')
    try {
      await page.goto('/settings')

      const deleteButton = page.getByRole('button', { name: 'Delete account' })
      await expect(deleteButton).toBeEnabled()

      // The typed confirmation: the confirm action stays disabled until the
      // username the account carries is typed back.
      await deleteButton.click()
      const dialog = page.getByRole('alertdialog')
      await expect(dialog).toBeVisible()
      const confirm = dialog.getByRole('button', { name: 'Delete account' })
      await expect(confirm).toBeDisabled()

      await dialog.locator('input').fill(account.username)
      await expect(confirm).toBeEnabled()
      await confirm.click()

      // The account is gone server-side; the browser is carried to login.
      await expect(page).toHaveURL(/\/login/, { timeout: 15_000 })

      // The credentials no longer admit anyone.
      const refusal = await request.post('/rpc/saka.authn.v1.AuthService/SignIn', {
        data: { identity: account.username, password: account.password }
      })
      expect(refusal.status()).toBeGreaterThanOrEqual(400)
      user = undefined
    } finally {
      await updateSetting(request, admin, 'users.self_delete_enabled', 'false')
    }
  })
})
