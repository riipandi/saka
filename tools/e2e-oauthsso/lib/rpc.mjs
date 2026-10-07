// The server the ladder walks against, and one ConnectRPC call over the
// shared JSON codec — the wire contract the ladder judges, headers included.
export const BASE = 'http://localhost:3000'

export async function rpc(method, body, token, reauth) {
  const headers = {
    'Content-Type': 'application/json',
    'Connect-Protocol-Version': '1'
  }
  if (token) headers.Authorization = `Bearer ${token}`
  if (reauth) headers['X-Saka-Reauthentication'] = reauth
  const r = await fetch(`${BASE}/rpc/${method}`, {
    method: 'POST',
    headers,
    body: JSON.stringify(body)
  })
  return { status: r.status, body: await r.json() }
}
