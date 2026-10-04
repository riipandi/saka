// A read into the database the server runs against — the ladder's window
// on the columns no wire surface answers (users.custom_attributes). The
// dev database's container is the one publishing 5432; its name is
// resolved from the running stack, not hardcoded to a project.
import { execFileSync } from 'node:child_process'

export function sql(query) {
  const name = execFileSync('docker', [
    'ps', '--filter', 'publish=5432', '--format', '{{.Names}}',
  ], { encoding: 'utf8' }).trim().split('\n')[0]
  if (!name) throw new Error('no postgres container publishes 5432')
  return execFileSync('docker', ['exec', name, 'psql', '-U', 'postgres', '-d', 'postgres', '-tAc', query],
    { encoding: 'utf8', timeout: 30_000 }).trim()
}
