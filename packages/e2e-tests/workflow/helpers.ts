import { expect, type APIRequestContext } from '@playwright/test'

/**
 * The helpers the account-flow E2E files share: wire seeding (an admin
 * Bearer and the accounts the acts need), the Mailpit reads the codes ride,
 * and the settings writes the gates demand.
 */

const identity = process.env.E2E_IDENTITY || 'admin'
const adminPassword = process.env.E2E_PASSWORD || '@dmin123'
export const MAILPIT = process.env.E2E_MAILPIT || 'http://127.0.0.1:8025/api/v1'

/** A 1×1 transparent PNG — the bytes the picture upload needs to carry. */
export const PNG_BYTES = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
  'base64'
)

/** Sign in over the wire and answer the Bearer the procedures judge. */
export async function adminToken(request: APIRequestContext): Promise<string> {
  const response = await request.post('/rpc/saka.authn.v1.AuthService/SignIn', {
    data: { identity, password: adminPassword }
  })
  expect(response.status()).toBe(200)
  return (await response.json()).access_token
}

export function bearer(token: string): Record<string, string> {
  return { authorization: `Bearer ${token}` }
}

export interface SeedUser {
  id: string
  username: string
  email: string
  password: string
}

/**
 * Create a throwaway account over the admin wire. The account is verified
 * at creation, so it can sign in without a mailed code.
 */
export async function createVerifiedUser(
  request: APIRequestContext,
  token: string,
  suffix: string
): Promise<SeedUser> {
  const username = `e2e_${suffix}`
  const email = `${username}@example.com`
  const password = 'e2e-password-1'
  const response = await request.post('/rpc/saka.identity.v1.UserService/CreateUser', {
    headers: bearer(token),
    data: {
      username,
      email,
      password,
      firstName: 'E2E',
      lastName: 'Throwaway',
      emailVerified: true
    }
  })
  expect(response.status()).toBe(200)
  const body = await response.json()
  expect(body.status).toBe('success')
  return { id: body.user.id, username, email, password }
}

/** The act's row leaves with the act. */
export async function deleteUser(
  request: APIRequestContext,
  token: string,
  id: string
): Promise<void> {
  const response = await request.post('/rpc/saka.identity.v1.UserService/DeleteUser', {
    headers: bearer(token),
    data: { id }
  })
  expect(response.status()).toBe(200)
}

/** Write one deployment setting as the administrator. */
export async function updateSetting(
  request: APIRequestContext,
  token: string,
  key: string,
  value: string
): Promise<void> {
  const response = await request.post('/rpc/saka.settings.v1.SettingsService/Update', {
    headers: bearer(token),
    data: { key, value }
  })
  expect(response.status()).toBe(200)
}

/** The decoded Mailpit list entry and message body the flow needs. */
interface MailpitMessage {
  messages?: Array<{ ID: string; Subject?: string; To?: Array<{ Address: string }> }>
}

interface MailpitDetail {
  Text?: string
  Subject?: string
}

/**
 * Sign the account in through the form and wait for the dashboard. Each
 * test owns a fresh context, so the signed-in tests carry their own
 * sign-in; the wait is generous because the login page warms its queries
 * before the form renders. The context is tracked for the file's
 * `afterEach` — a lingering context's page steals the failure snapshot a
 * later test attaches its error to.
 */
const trackedContexts: import('@playwright/test').BrowserContext[] = []

export async function signIn(
  browser: import('@playwright/test').Browser,
  account: { username: string; password: string }
): Promise<import('@playwright/test').Page> {
  const context = await browser.newContext()
  trackedContexts.push(context)
  const page = await context.newPage()
  await page.goto('/login')
  await page.fill('#identity', account.username)
  await page.fill('#password', account.password)
  await page.click('button[type=submit]')
  await expect(page).toHaveURL(/\/overview$/, { timeout: 15_000 })
  return page
}

/** Close every sign-in context the file's tests opened. */
export async function closeSignInContexts(): Promise<void> {
  for (const context of trackedContexts.splice(0)) {
    await context.close()
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

/**
 * The newest message whose subject matches, read as text. The queue's
 * delivery is asynchronous, so the read polls until the mail lands or the
 * patience runs out — a caller that needs the mail needs it to exist.
 */
export async function latestMail(
  subjectPattern: RegExp,
  toAddress?: string,
  patienceMs = 10_000
): Promise<{ subject: string; text: string; to: string } | null> {
  const deadline = Date.now() + patienceMs
  for (;;) {
    const found = await readMailbox(subjectPattern, toAddress)
    if (found || Date.now() > deadline) return found
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
}

async function readMailbox(
  subjectPattern: RegExp,
  toAddress?: string
): Promise<{ subject: string; text: string; to: string } | null> {
  const response = await fetch(`${MAILPIT}/messages?limit=25`)
  const parsed: unknown = await response.json()
  if (!isRecord(parsed)) return null
  const data = parsed as MailpitMessage
  for (const summary of data.messages ?? []) {
    const to = summary.To?.map((entry) => entry.Address).join(',') ?? ''
    if (toAddress && !to.includes(toAddress)) continue
    if (summary.Subject && !subjectPattern.test(summary.Subject)) continue
    const raw = await (await fetch(`${MAILPIT}/message/${summary.ID}`)).json()
    if (!isRecord(raw)) continue
    const detail = raw as MailpitDetail
    return { subject: detail.Subject ?? summary.Subject ?? '', text: detail.Text ?? '', to }
  }
  return null
}

/**
 * The 12-character code the message carries, read out of the plain text.
 * The code is the standalone alphanumeric line in the body; the prose's
 * long words ("verification") must not match, so the line anchors it.
 */
export function codeFromMail(text: string): string | null {
  const lines = text
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => /^[A-Za-z0-9]{12,26}$/.test(line))
  const line = lines[lines.length - 1]
  return line ? line.slice(0, 12) : null
}

/**
 * Fill the twelve-slot code entry: the first slot takes the focus, the
 * keystrokes walk the rest. The slots are the only visible inputs of their
 * kind on the page.
 */
export async function typeCode(page: import('@playwright/test').Page, code: string): Promise<void> {
  // Base UI renders a visible slot row and a hidden paste target beside it;
  // both answer the maxlength query, so the first — the focused slot — is
  // the one the keystrokes start from.
  const firstSlot = page.locator('input[maxlength="12"]').first()
  await firstSlot.click()
  await page.keyboard.type(code, { delay: 20 })
}
