import { test, expect, type APIRequestContext } from '@playwright/test'
import { adminToken, bearer, createVerifiedUser, deleteUser, type SeedUser } from './helpers'

// The upload surface on a real deployment: two accounts drive /api/uploads
// against one key. A stored file is never replaced or deleted through the
// protocol, another account's session is invisible, and what /storage
// serves back carries the sandbox policy and downloads instead of rendering.

const BUCKET = 'devbucket'

let admin: string
let alice: SeedUser | undefined
let bob: SeedUser | undefined
let aliceToken = ''
let bobToken = ''

async function tokenFor(request: APIRequestContext, account: SeedUser): Promise<string> {
  const response = await request.post('/rpc/saka.authn.v1.AuthService/SignIn', {
    data: { identity: account.username, password: account.password }
  })
  expect(response.status()).toBe(200)
  return (await response.json()).access_token
}

const meta = (pairs: Record<string, string>) =>
  Object.entries(pairs)
    .map(([name, value]) => `${name} ${Buffer.from(value).toString('base64')}`)
    .join(',')

function create(
  request: APIRequestContext,
  token: string,
  key: string,
  length: number,
  body: string,
  filetype = 'text/html'
) {
  return request.post('/api/uploads', {
    headers: {
      ...bearer(token),
      'tus-resumable': '1.0.0',
      'upload-length': String(length),
      'upload-metadata': meta({ bucket: BUCKET, key, filetype })
    },
    data: Buffer.from(body)
  })
}

const sessionPath = (key: string) => `/api/uploads/${BUCKET}/${key}`

test.describe.serial('the upload ownership flow', () => {
  const stamp = Date.now() % 1_000_000
  const finalKey = `e2e/${stamp}-final.html`
  const sessionKey = `e2e/${stamp}-session.txt`

  test.beforeAll(async ({ request }) => {
    admin = await adminToken(request)
    alice = await createVerifiedUser(request, admin, `upa${stamp % 100000}`)
    bob = await createVerifiedUser(request, admin, `upb${stamp % 100000}`)
    aliceToken = await tokenFor(request, alice)
    bobToken = await tokenFor(request, bob)
  })

  test.afterAll(async ({ request }) => {
    if (alice) await deleteUser(request, admin, alice.id)
    if (bob) await deleteUser(request, admin, bob.id)
  })

  test('a stored upload cannot be replaced or deleted by anyone, and is served sandboxed', async ({
    request
  }) => {
    const created = await create(request, aliceToken, finalKey, 8, '<b>hi</b>')
    expect(created.status()).toBe(201)

    // The queue settles the file; the mount answers once it has.
    await expect
      .poll(async () => (await request.get(`/storage/${BUCKET}/${finalKey}`)).status(), {
        timeout: 20_000
      })
      .toBe(200)

    expect((await create(request, bobToken, finalKey, 3, 'bob')).status()).toBe(409)
    expect((await create(request, aliceToken, finalKey, 3, 'me!')).status()).toBe(409)
    for (const token of [aliceToken, bobToken]) {
      const deleted = await request.delete(sessionPath(finalKey), { headers: bearer(token) })
      expect(deleted.status()).toBe(404)
    }

    const served = await request.get(`/storage/${BUCKET}/${finalKey}`)
    expect(served.status()).toBe(200)
    expect(await served.text()).toBe('<b>hi</b>')
    expect(served.headers()['content-security-policy']).toContain('sandbox')
    expect(served.headers()['content-disposition']).toBe('attachment')
  })

  test("another account cannot see, extend or end someone else's session", async ({ request }) => {
    expect(
      (await create(request, aliceToken, sessionKey, 10, '01234', 'text/plain')).status()
    ).toBe(201)

    expect((await create(request, bobToken, sessionKey, 10, '', 'text/plain')).status()).toBe(409)
    expect(
      (await request.head(sessionPath(sessionKey), { headers: bearer(bobToken) })).status()
    ).toBe(404)
    const patched = await request.patch(sessionPath(sessionKey), {
      headers: {
        ...bearer(bobToken),
        'content-type': 'application/offset+octet-stream',
        'upload-offset': '5'
      },
      data: Buffer.from('56789')
    })
    expect(patched.status()).toBe(404)
    expect(
      (await request.delete(sessionPath(sessionKey), { headers: bearer(bobToken) })).status()
    ).toBe(404)

    const head = await request.head(sessionPath(sessionKey), { headers: bearer(aliceToken) })
    expect(head.status()).toBe(200)
    expect(head.headers()['upload-offset']).toBe('5')
    expect(
      (await request.delete(sessionPath(sessionKey), { headers: bearer(aliceToken) })).status()
    ).toBe(204)
  })
})
