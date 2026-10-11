import { test, expect } from '@playwright/test'
import { adminToken, bearer, closeSignInContexts, deleteUser, updateSetting } from './helpers'

// The public signup on a real deployment: the invite token joins the
// account to the token's groups, a tokenless submit is refused at the
// form, a blocklisted address is refused with the service's word, and a
// duplicate account answers the duplicate's word. The gates ride the
// settings the administrator writes.

let admin: string
let groupId: string
let rawToken: string
let createdUserId: string | undefined

/** The wire's signup call, the same one the form sends. */
async function wireSignup(
  request: import('@playwright/test').APIRequestContext,
  payload: Record<string, unknown>
): Promise<{ status: number; message?: string }> {
  const response = await request.post('/rpc/saka.identity.v1.SignupService/Signup', {
    data: payload
  })
  const parsed: unknown = await response.json()
  const body = isRecord(parsed) ? parsed : undefined
  return {
    status: response.status(),
    message: typeof body?.message === 'string' ? body.message : undefined
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

test.describe.serial('the signup flow', () => {
  test.afterEach(async () => {
    await closeSignInContexts()
  })

  test.beforeAll(async ({ request }) => {
    admin = await adminToken(request)

    // The invitation shape: invite mode, no verification gate, the blocklist
    // off, and one group the token's account will join.
    await updateSetting(request, admin, 'access.mode', 'invite')
    await updateSetting(request, admin, 'auth.verify_email_at_signup', 'false')
    await updateSetting(request, admin, 'access.blocklist_enabled', 'false')
    await updateSetting(request, admin, 'auth.signup_username_enabled', 'true')
    await updateSetting(request, admin, 'auth.require_username', 'true')

    const group = await request.post('/rpc/saka.identity.v1.UserGroupService/CreateUserGroup', {
      headers: bearer(admin),
      data: { name: `e2e_invite_${Date.now() % 100000}`, displayName: 'E2E Invitees' }
    })
    expect(group.status()).toBe(200)
    groupId = (await group.json()).group.id

    const issued = await request.post('/rpc/saka.identity.v1.SignupService/CreateSignupToken', {
      headers: bearer(admin),
      data: { ttlSeconds: 3600, userGroupIds: [groupId] }
    })
    expect(issued.status()).toBe(200)
    rawToken = (await issued.json()).raw_token
  })

  test.afterAll(async ({ request }) => {
    if (createdUserId) await deleteUser(request, admin, createdUserId)
    if (groupId) {
      await request.post('/rpc/saka.identity.v1.UserGroupService/DeleteUserGroup', {
        headers: bearer(admin),
        data: { id: groupId }
      })
    }
    // The deployment's gates return to their catalog defaults — every
    // surface reading these settings (the email flow's verification mail
    // among them) sees the default deployment again.
    await updateSetting(request, admin, 'access.mode', 'open')
    await updateSetting(request, admin, 'access.blocklist_enabled', 'false')
    await updateSetting(request, admin, 'auth.verify_email_at_signup', 'true')
    await updateSetting(request, admin, 'auth.signup_username_enabled', 'false')
    await updateSetting(request, admin, 'auth.require_username', 'false')
  })

  test("an invited signup joins the token's groups", async ({ browser, request }) => {
    const context = await browser.newContext()
    const page = await context.newPage()

    // The invitation link carries the raw token; the form prefills it.
    await page.goto(`/signup?token=${rawToken}`)
    await expect(page.getByText('Create your account')).toBeVisible({ timeout: 15_000 })
    await page.getByLabel('First name').fill('E2E')
    await page.getByLabel('Last name').fill('Invitee')
    await page.getByLabel('Username').fill(`e2e_inv${Date.now() % 100000}`)
    await page.getByLabel('Email').fill(`e2e_inv${Date.now() % 100000}@example.com`)
    await page.getByRole('textbox', { name: 'Password' }).fill('e2e-password-1')
    await page.getByRole('button', { name: 'Create account' }).click()

    // The created account lands on the sign-in door — no verification gate
    // is armed — and the wire names the group the token carried.
    await expect(page).toHaveURL(/\/login$/, { timeout: 15_000 })

    const list = await request.post('/rpc/saka.identity.v1.UserService/ListUsers', {
      headers: bearer(admin),
      data: { search: 'E2E Invitee', page: 1, limit: 5 }
    })
    expect(list.status()).toBe(200)
    const found = (await list.json()).users?.[0]
    expect(found).toBeTruthy()
    createdUserId = found.id
    expect(found.user_groups?.map((entry: { id: string }) => entry.id)).toContain(groupId)
    await context.close()
  })

  test('invite mode refuses a tokenless submit at the form', async ({ browser }) => {
    const context = await browser.newContext()
    const page = await context.newPage()
    await page.goto('/signup')
    await page.getByLabel('First name').fill('E2E')
    await page.getByLabel('Last name').fill('Tokenless')
    await page.getByLabel('Username').fill('e2e_tokenless')
    await page.getByLabel('Email').fill(`e2e_tokenless_${Date.now() % 100000}@example.com`)
    await page.getByRole('textbox', { name: 'Password' }).fill('e2e-password-1')
    await page.getByRole('button', { name: 'Create account' }).click()

    // The empty token is the form's refusal, not the server's — no wire
    // call is made, so no account could have been created.
    await expect(page.getByText('Enter your invitation token.')).toBeVisible({ timeout: 15_000 })
    expect(page.url()).not.toContain('/login')
    await context.close()
  })

  test("a blocklisted address refuses with the service's word", async ({ request, browser }) => {
    const context = await browser.newContext()
    const page = await context.newPage()

    // The blocklist arms in the open mode; the entry names a domain.
    await updateSetting(request, admin, 'access.mode', 'open')
    await updateSetting(request, admin, 'access.blocklist_enabled', 'true')
    const entry = await request.post('/rpc/saka.identity.v1.BlocklistService/AddBlocklistEntry', {
      headers: bearer(admin),
      data: { pattern: '@blocked.example' }
    })
    expect(entry.status()).toBe(200)
    const entryId = (await entry.json()).entry.id

    const refused = await wireSignup(request, {
      firstName: 'E2E',
      lastName: 'Blocked',
      username: 'e2e_blocked',
      email: 'stranger@blocked.example',
      password: 'e2e-password-1'
    })
    expect(refused.status).toBe(404)
    expect(refused.message).toBe('sign-up is not available')

    // The form carries the server's sentence verbatim.
    await page.goto('/signup')
    await page.getByLabel('First name').fill('E2E')
    await page.getByLabel('Last name').fill('Blocked')
    await page.getByLabel('Username').fill('e2e_blocked')
    await page.getByLabel('Email').fill('stranger@blocked.example')
    await page.getByRole('textbox', { name: 'Password' }).fill('e2e-password-1')
    await page.getByRole('button', { name: 'Create account' }).click()
    await expect(page.getByText('sign-up is not available')).toBeVisible({ timeout: 15_000 })

    const removal = await request.post(
      '/rpc/saka.identity.v1.BlocklistService/RemoveBlocklistEntry',
      {
        headers: bearer(admin),
        data: { id: entryId }
      }
    )
    expect(removal.status()).toBe(200)
    await updateSetting(request, admin, 'access.blocklist_enabled', 'false')
    await context.close()
  })

  test("a duplicate account answers the duplicate's word", async ({ request }) => {
    const username = `e2e_dup${Date.now() % 100000}`
    const email = `${username}@example.com`
    const account = {
      firstName: 'E2E',
      lastName: 'Duplicate',
      username,
      email,
      password: 'e2e-password-1'
    }

    const first = await wireSignup(request, account)
    expect(first.status).toBe(200)

    // Bulk enumeration protection answers the plain refusal.
    const second = await wireSignup(request, account)
    expect(second.status).toBe(409)
    expect(second.message).toBe('account already exists')

    // The duplicate's refusal leaves no row behind; the created account is
    // the cleanup's own.
    const list = await request.post('/rpc/saka.identity.v1.UserService/ListUsers', {
      headers: bearer(admin),
      data: { search: username, page: 1, limit: 5 }
    })
    const found = (await list.json()).users?.[0]
    if (found) await deleteUser(request, admin, found.id)
  })
})
