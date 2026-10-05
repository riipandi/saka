// The ConnectRPC client the scenarios share: one POST per procedure, the
// envelope parsed, the answer checked in the caller's terms. The wire here is
// exactly what the web frontend sends — the JSON codec with proto field
// names, the bearer on the authenticated calls.

import http from 'k6/http'
import { BASE_URL, IDENTITY, PASSWORD } from './config.mjs'

// RPCPath is the prefix the ConnectRPC surface mounts at.
export const RPCPath = '/rpc'

const contentType = 'application/json'

// unwrap reads the wire: ConnectRPC answers the message itself — the JSON
// codec's proto field names, no envelope — and refuses with `{code, message}`.
// A response carrying the error pair is the refusal; anything else is the
// data.
function unwrap(res) {
  if (res.status === 0) {
    return { ok: false, code: 'network', data: null, status: 0 }
  }
  let body = {}
  try {
    body = res.json()
  } catch {
    return { ok: false, code: 'unparseable', data: null, status: res.status }
  }
  if (body.code !== undefined && body.message !== undefined) {
    return { ok: false, code: body.code, data: null, status: res.status }
  }
  return { ok: true, code: '', data: body ?? {}, status: res.status }
}

// callRPC issues one procedure call. `token` (optional) rides the bearer —
// the same credential the browser's fetch interceptor attaches. The result is
// an object with `ok`, the unwrapped `data`, and the code a refusal named, so
// the caller's checks read intent rather than transport.
export function callRPC(t, procedure, payload, token) {
  const headers = {
    'Content-Type': contentType,
    'Connect-Protocol-Version': '1'
  }
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }
  const res = http.post(`${BASE_URL}${RPCPath}${procedure}`, JSON.stringify(payload ?? {}), {
    headers,
    tags: { name: t }
  })
  const out = unwrap(res)
  const checks = {
    'transport answered 200': res.status === 200
  }
  if (out.ok) {
    checks['envelope is success'] = true
  } else {
    checks[`refused: ${out.code}`] = false
  }
  return { ...out, checks }
}

// get is the plain-GET half of the surface: the public endpoints a browser
// or a monitor reaches without a credential.
export function get(t, path, token) {
  const headers = {}
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }
  return http.get(`${BASE_URL}${path}`, { headers, tags: { name: t } })
}

// The procedure paths, spelled once. A rename in the proto surfaces here as
// a refused call naming the old path.
export const procedures = {
  signIn: '/saka.authn.v1.AuthService/SignIn',
  getSession: '/saka.authn.v1.SessionService/GetSession',
  listSessions: '/saka.authn.v1.SessionService/ListSessions',
  refresh: '/saka.authn.v1.SessionService/Refresh',
  listNotifications: '/saka.notification.v1.NotificationService/ListNotifications',
  auditlogList: '/saka.auditlog.v1.AuditLogService/List',
  listQueues: '/saka.system.v1.QueueService/ListQueues',
  listTasks: '/saka.system.v1.QueueService/ListTasks',
  createBucket: '/saka.storage.v1.BucketService/CreateBucket'
}

// signIn exchanges the identity and password for a token pair. The MFA fork
// is a refusal here by contract: a load test account carries no second
// factor, and one that does fails the run naming the code it answered.
export function signIn() {
  const out = callRPC('signin', procedures.signIn, {
    identity: IDENTITY,
    password: PASSWORD
  })
  if (!out.ok) {
    return { ...out, accessToken: '', refreshToken: '' }
  }
  return {
    ...out,
    accessToken: out.data.access_token ?? '',
    refreshToken: out.data.refresh_token ?? ''
  }
}
