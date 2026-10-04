// Step 0b — the rows a previous ladder run left behind leave first, so
// every probe meets a clean surface.
import { rpc } from '../lib/rpc.mjs'
import { deleteConnections } from '../lib/admin.mjs'
import { state } from '../lib/state.mjs'

export async function run() {
  const { body } = await rpc('saka.identity.v1.UserService/ListUsers', {}, state.admin)
  for (const user of body.users ?? []) {
    if ((user.email ?? '').endsWith('hogwarts.example')) {
      await rpc('saka.identity.v1.UserService/DeleteUser', { id: user.id }, state.admin)
    }
  }
  await deleteConnections()
}
