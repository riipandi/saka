// The SignIn call the runner mints its bearer with — the same RPC the
// backoffice signs in through, over the shared JSON codec.

export async function signIn(apiBase, user, pass) {
  const response = await fetch(`${apiBase}/rpc/saka.authn.v1.AuthService/SignIn`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ identity: user, password: pass })
  })
  if (!response.ok) {
    throw new Error(`sign-in failed: HTTP ${response.status} ${await response.text()}`)
  }
  const body = await response.json()
  if (!body.access_token) {
    throw new Error(`sign-in answered no access token: ${JSON.stringify(body).slice(0, 200)}`)
  }
  return body.access_token
}
