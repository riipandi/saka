import { test, expect, type BrowserContext } from '@playwright/test'
import { adminToken, createVerifiedUser, deleteUser, type SeedUser } from './helpers'

// The session center on a real deployment: one account, two browser
// contexts, and the bulk writes the page carries. Sign-out-others ends the
// other context's session (the eviction lands on that browser's next
// navigation); sign-out-all ends the caller's own and carries the browser
// to login. The per-row revoke is Wave 2's — the page has no revoke button,
// and the E2E holds that absence too.

let admin: string
let user: SeedUser
let contextA: BrowserContext

test.describe.serial('the account sessions flow', () => {
  test.beforeAll(async ({ request, browser }) => {
    admin = await adminToken(request)
    user = await createVerifiedUser(request, admin, `sess${Date.now() % 100000}`)

    // Context A signs in first; context B's later sign-in is the second
    // session the page then lists.
    contextA = await browser.newContext()
    const page = await contextA.newPage()
    await page.goto('/login')
    await page.fill('#identity', user.username)
    await page.fill('#password', user.password)
    await page.click('button[type=submit]')
    await expect(page).toHaveURL(/\/overview$/)
  })

  test.afterAll(async ({ request }) => {
    await deleteUser(request, admin, user.id)
  })

  test('sign-out-others ends the second context from the first', async ({ browser }) => {
    // Context A holds the session the act will keep; context B signs in as
    // the same account — the second row the page lists.
    const second = await browser.newContext()
    const pageB = await second.newPage()
    await pageB.goto('/login')
    await pageB.fill('#identity', user.username)
    await pageB.fill('#password', user.password)
    await pageB.click('button[type=submit]')
    await expect(pageB).toHaveURL(/\/overview$/)

    // B reads the center: two rows, exactly one of them this device's.
    await pageB.goto('/account/sessions')
    await expect(pageB.getByText('This device')).toHaveCount(1)
    await expect(pageB.getByText('Live')).toBeVisible()

    // The bulk write asks once and answers with what the server carried:
    // every session but B's ends.
    await pageB.getByRole('button', { name: 'Sign out others' }).click()
    const dialog = pageB.getByRole('alertdialog')
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: 'Sign out others' }).click()
    await expect(dialog).toBeHidden()
    await expect(pageB).toHaveURL(/\/account\/sessions$/)

    // The eviction is the other context's problem to discover: A's session
    // is dead server-side, and its next navigation answers it as such.
    const pageA = contextA.pages()[0]
    if (!pageA) throw new Error('the first context lost its page')
    await pageA.goto('/overview')
    await expect(pageA).toHaveURL(/\/login/)
  })

  test('sign-out-all ends the caller too and lands on login', async ({ browser }) => {
    const context = await browser.newContext()
    const page = await context.newPage()
    await page.goto('/login')
    await page.fill('#identity', user.username)
    await page.fill('#password', user.password)
    await page.click('button[type=submit]')
    await expect(page).toHaveURL(/\/overview$/)

    await page.goto('/account/sessions')
    await page.getByRole('button', { name: 'Sign out all' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: 'Sign out all' }).click()

    // The pair is dropped client-side and the shell's eviction effect
    // carries the browser away.
    await expect(page).toHaveURL(/\/login/, { timeout: 15_000 })
  })

  test('the page carries no per-row revoke', async ({ browser }) => {
    const context = await browser.newContext()
    const page = await context.newPage()
    await page.goto('/login')
    await page.fill('#identity', user.username)
    await page.fill('#password', user.password)
    await page.click('button[type=submit]')
    await expect(page).toHaveURL(/\/overview$/)

    await page.goto('/account/sessions')
    await expect(page.getByRole('button', { name: 'Revoke' })).toHaveCount(0)
  })
})
