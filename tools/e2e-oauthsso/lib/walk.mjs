// The browser flow with redirects held at arm's length: the start route's
// 302 is the authorize URL, the mock's login page takes a username alone,
// and the callback lands back on the SPA with the flow token in its
// query. Cookies the mock sets travel forward, the way a browser's jar
// would carry them.
import { BASE } from './rpc.mjs'

export async function walk(provider, subject) {
  const jar = new Map()

  async function open(url, init = {}) {
    const headers = new Headers(init.headers)
    const cookie = [...jar.entries()].map(([k, v]) => `${k}=${v}`).join('; ')
    if (cookie) headers.set('Cookie', cookie)
    const r = await fetch(url, { ...init, headers, redirect: 'manual' })
    for (const raw of r.headers.getSetCookie?.() ?? []) {
      const [pair] = raw.split(';')
      const eq = pair.indexOf('=')
      if (eq > 0) jar.set(pair.slice(0, eq).trim(), pair.slice(eq + 1).trim())
    }
    return r
  }

  const start = await open(`${BASE}/oauth/${provider}/start`)
  const authorize = start.headers.get('Location')
  if (start.status !== 302 || !authorize) throw new Error(`start refused: ${start.status}`)

  const page = await open(authorize)
  if (page.status !== 200) throw new Error(`authorize page refused: ${page.status}`)

  const login = await open(authorize, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ username: subject }).toString(),
  })
  const callback = login.headers.get('Location')
  if (login.status !== 302 || !callback?.includes('code=')) {
    throw new Error(`login refused: ${login.status} ${String(callback).slice(0, 120)}`)
  }

  const landing = await open(callback)
  const location = landing.headers.get('Location') ?? ''
  const qs = new URLSearchParams(location.split('?')[1] ?? '')
  if (landing.status !== 302 || !qs.has('flow_token')) {
    throw new Error(`callback failed: ${location.slice(0, 120)}`)
  }
  return qs.get('flow_token')
}
