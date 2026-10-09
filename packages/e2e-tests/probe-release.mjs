#!/usr/bin/env node
// Phase 9 release probe: the composition root against a freshly built
// `build/release/saka serve --env-file=.env.local` on :3000.
// Covers the plan's probe list: profile save, picture upload,
// sign-out-others from a second context, verify-email via Mailpit,
// audit paging. Restores everything it touches.

const base = 'http://localhost:3000'
const mailpit = 'http://127.0.0.1:8025'
const stamp = `probe${Math.floor(Math.random() * 90000 + 10000)}`
let ok = 0
let bad = 0

function record(name, pass, detail = '') {
  if (pass) { ok += 1; console.log(`ok   ${name}${detail ? ' — ' + detail : ''}`) } else { bad += 1; console.log(`FAIL ${name}${detail ? ' — ' + detail : ''}`) }
}

async function rpc(path, token, body) {
  const res = await fetch(base + path, {
    method: 'POST',
    headers: { 'content-type': 'application/json', ...(token ? { authorization: `Bearer ${token}` } : {}) },
    body: JSON.stringify(body)
  })
  return res
}

async function rpcJson(path, token, body) {
  const res = await rpc(path, token, body)
  const text = await res.text()
  let json = null
  try { json = JSON.parse(text) } catch { /* raw body */ }
  return { status: res.status, json, text }
}

async function signIn(identity, password) {
  const { status, json } = await rpcJson('/rpc/saka.authn.v1.AuthService/SignIn', null, { identity, password })
  if (status !== 200) throw new Error(`sign-in ${identity} → ${status} ${JSON.stringify(json)}`)
  return json.access_token ?? json.accessToken
}

const admin = await signIn('admin', '@dmin123')
record('admin signs in', Boolean(admin))

// 1 — profile save
const me = await rpcJson('/rpc/saka.identity.v1.UserService/GetCurrentUser', admin, {})
const before = me.json?.user ?? {}
const saved = await rpcJson('/rpc/saka.identity.v1.UserService/UpdateCurrentUser', admin, {
  first_name: before.first_name ?? 'Admin',
  last_name: before.last_name ?? 'Sistem',
  display_name: `Admin Sistem ${stamp}`,
  locale: before.locale ?? 'en',
  timezone: before.timezone ?? 'Asia/Jakarta'
})
record('UpdateCurrentUser answers the change', saved.status === 200 && (saved.json?.user?.display_name ?? '').includes(stamp), `status=${saved.status}`)
const restore = await rpcJson('/rpc/saka.identity.v1.UserService/UpdateCurrentUser', admin, {
  first_name: before.first_name ?? 'Admin',
  last_name: before.last_name ?? 'Sistem',
  display_name: 'Admin Sistem',
  locale: before.locale ?? 'en',
  timezone: before.timezone ?? 'Asia/Jakarta'
})
record('admin display name restored', restore.status === 200 && restore.json?.user?.display_name === 'Admin Sistem')

// 2 — picture upload (raw body) + public read
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64')
const put = await fetch(base + '/api/users/me/profile-picture', {
  method: 'PUT',
  headers: { 'content-type': 'image/png', authorization: `Bearer ${admin}` },
  body: png
})
record('picture PUT accepts a PNG', put.status === 200, `status=${put.status}`)
const me2 = await rpcJson('/rpc/saka.identity.v1.UserService/GetCurrentUser', admin, {})
const pic = me2.json?.user?.picture ?? ''
record('view carries the picture URL', pic.includes('/storage/'), pic)
const pngGet = await fetch(pic)
record('picture read streams bytes', pngGet.status === 200 && (pngGet.headers.get('content-type') ?? '').includes('image'), `status=${pngGet.status}`)
const reset = await fetch(base + '/rpc/saka.identity.v1.UserService/ResetProfilePicture', {
  method: 'POST',
  headers: { 'content-type': 'application/json', authorization: `Bearer ${admin}` },
  body: JSON.stringify({ id: before.id })
})
const me3 = await rpcJson('/rpc/saka.identity.v1.UserService/GetCurrentUser', admin, {})
record('reset clears the picture', reset.status === 200 && !(me3.json?.user?.picture ?? '').includes('/storage/'), `status=${reset.status} picture=${me3.json?.user?.picture ?? '(absent)'}`)

// 3 — sign-out-others from a second context. The old ACCESS token is
// stateless and keeps working until its own expiry; the eviction the E2E
// observes lands when the holder's next read touches its session row, so the
// probe reads the session, not the profile.
const second = await signIn('admin', '@dmin123')
const others = await rpcJson('/rpc/saka.authn.v1.SessionService/SignOutOtherSessions', second, {})
record('SignOutOtherSessions answers', others.status === 200, `status=${others.status}`)
const evicted = await rpcJson('/rpc/saka.authn.v1.SessionService/GetSession', admin, {})
record('first context evicted on next session read', evicted.status === 401, `status=${evicted.status}`)

// 4 — verify-email via Mailpit, through the signup door: an account the
// deployment refuses sign-in for cannot call SendEmail, so the code the
// signup mails is the public path — the same one the verify route serves.
const password = `Pr${stamp}be!x9Q`
const inv = await rpcJson('/rpc/saka.identity.v1.SignupService/CreateSignupToken', second, { ttlSeconds: 3600 })
record('signup token minted', inv.status === 200, `status=${inv.status}`)
const rawToken = inv.json?.raw_token
const signed = await rpcJson('/rpc/saka.identity.v1.SignupService/Signup', null, {
  username: stamp,
  email: `${stamp}@probe.local`,
  password,
  token: rawToken,
  first_name: 'Probe',
  last_name: 'Release'
})
const uid = signed.json?.user?.id
record('signup created the account', signed.status === 200 && Boolean(uid), `status=${signed.status} id=${uid ?? '-'}`)

let code = ''
for (let i = 0; i < 20; i += 1) {
  await new Promise(r => setTimeout(r, 500))
  const list = await (await fetch(`${mailpit}/api/v1/search?query=to:${stamp}@probe.local`)).json()
  const hit = (list.messages ?? []).find(m => /verify/i.test(m.Subject))
  if (!hit) continue
  const detail = await (await fetch(`${mailpit}/api/v1/message/${hit.ID}`)).json()
  // The code stands alone on its own line; a bare 12-char regex would grab
  // any long word in the prose ("verification" included).
  const line = String(detail.Text ?? '')
    .split(/\r?\n/)
    .map(l => l.trim())
    .filter(l => /^[A-Za-z0-9]{12,26}$/.test(l))
    .pop()
  if (line) { code = line.slice(0, 12); break }
}
record('Mailpit carries the 12-char code', code.length === 12, code ? `${code.slice(0, 4)}…` : '(none)')
const verified = await rpcJson('/rpc/saka.identity.v1.EmailVerificationService/VerifyEmail', null, { token: code })
record('VerifyEmail consumes the code', verified.status === 200, `status=${verified.status}`)
const again = await rpcJson('/rpc/saka.identity.v1.EmailVerificationService/VerifyEmail', null, { token: code })
record('replay answers failure', again.status !== 200, `status=${again.status}`)
const reSignIn = await signIn(stamp, password)
record('the now-verified account signs in', Boolean(reSignIn))

// 5 — audit paging (as the probe account, whose acts just happened)
const page1 = await rpcJson('/rpc/saka.auditlog.v1.AuditLogService/List', reSignIn, { page: 1, limit: 5 })
const page2 = await rpcJson('/rpc/saka.auditlog.v1.AuditLogService/List', reSignIn, { page: 2, limit: 5 })
const first1 = page1.json?.logs?.[0]?.id ?? page1.json?.logs?.[0]?.event ?? ''
const first2 = page2.json?.logs?.[0]?.id ?? page2.json?.logs?.[0]?.event ?? ''
record('audit page 1 answers records', (page1.json?.logs ?? []).length > 0, `count=${(page1.json?.logs ?? []).length}`)
record('page 2 differs from page 1', page1.status === 200 && page2.status === 200 && first1 !== first2, `${first1} vs ${first2}`)

// 6 — cleanup
const del = await rpcJson('/rpc/saka.identity.v1.UserService/DeleteUser', second, { id: uid })
record('throwaway deleted', del.status === 200, `status=${del.status}`)

console.log(`\n${ok} ok, ${bad} failed`)
process.exit(bad ? 1 : 0)
