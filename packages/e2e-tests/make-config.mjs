// The E2E harness's config: the deployment's own app.config.json with the
// rate budgets the suite needs raised. The suite's sign-ins are its seed
// work, not an attacker's script — the credential bucket's default budget
// (ten a minute) is one the run outruns in its first minute. The file is
// written beside the binary the webServer builds and serves through
// --config-file; nothing here is committed state.
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { resolve, dirname } from 'node:path'

const buildTag = process.env.E2E_BUILD_TAG || 'release'
const root = resolve(import.meta.dirname, '..', '..')
const source = JSON.parse(readFileSync(resolve(root, 'app.config.json'), 'utf8'))

source.rate_limit = {
  ...source.rate_limit,
  driver: source.rate_limit?.driver ?? 'database',
  limit: 200,
  auth_limit: 200,
  // The config layer reads duration keys as seconds.
  window: 60
}

// The one-time access email path is the sign-in completeness suite's act;
// the deployment ships it off by default (the mailer must not be something
// anyone on the internet can drive), so the harness turns it on here only.
source.auth = {
  ...source.auth,
  one_time_access_email_as_unauthenticated_enabled: true
}

const target = resolve(root, 'build', buildTag, 'app.config.e2e.json')
mkdirSync(dirname(target), { recursive: true })
writeFileSync(target, JSON.stringify(source, null, 2))
console.log('e2e config written:', target)
