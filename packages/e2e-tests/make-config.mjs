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

// The stored-file origin rides the deployment's `PUBLIC_ASSETS_URL`, which
// pins :3000 — the picture URLs the suite's accounts answer would point at
// an origin nothing serves on the dedicated port. The harness re-pins it to
// its own server so the avatar's img resolves.
source.app = {
  ...source.app,
  assets_url: `http://localhost:${process.env.E2E_PORT || 4473}/storage`
}

// The interaction suite walks the provider's protocol surface — the
// authorize redirects, the device verification, the SPA's consent page —
// so the federation switch rides on for the run.
source.oidc = {
  ...source.oidc,
  enabled: true
}

const target = resolve(root, 'build', buildTag, 'app.config.e2e.json')
mkdirSync(dirname(target), { recursive: true })
writeFileSync(target, JSON.stringify(source, null, 2))
console.log('e2e config written:', target)
