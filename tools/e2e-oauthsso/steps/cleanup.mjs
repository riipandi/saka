import { getUser, deleteConnections } from '../lib/admin.mjs'
import { check } from '../lib/check.mjs'
// Step 15 — the rows the ladder created leave in the same turn: the
// accounts, then the connections its ensure step wrote.
import { rpc } from '../lib/rpc.mjs'
import { state } from '../lib/state.mjs'

export async function run() {
  console.log('15. Cleanup: the rows the ladder created leave in the same turn')
  {
    const { body } = await rpc('saka.identity.v1.UserService/ListUsers', {}, state.admin)
    for (const user of body.users ?? []) {
      if ((user.email ?? '').endsWith('hogwarts.example')) {
        await rpc('saka.identity.v1.UserService/DeleteUser', { id: user.id }, state.admin)
      }
    }
    const left = await getUser('mapped-padma@hogwarts.example')
    check('13a. the E2E accounts are gone', left === null, JSON.stringify(left ?? {}).slice(0, 160))
    await deleteConnections()
    const conns = await rpc('saka.authn.v1.OAuthSSOService/ListConnections', {}, state.admin)
    const strays = (conns.body.connections ?? []).filter((c) =>
      (c.provider ?? '').startsWith('mock-')
    )
    check(
      '13b. the E2E connections are gone',
      strays.length === 0,
      JSON.stringify(strays).slice(0, 160)
    )
  }
}
