#!/usr/bin/env node
// The OAuth SSO E2E ladder against a running release binary and the
// navikt mock (container/compose-dev.yaml). Every branch the plan names,
// over the wire, ending with the created rows removed. Node's own
// modules only — no dependency to install. The wire helpers live in
// `lib/`, the probes in `steps/`; this file is the run order alone.
//
// Run (see the handover): `task build`, the compose services up
// (pgsql, mailpit, mock-oauth2, nginx), the dev database reset and
// seeded, and the release binary serving with a throwaway config file
// that raises `rate_limit.limit` and `rate_limit.auth_limit` — the
// credential bucket trips at ten a minute otherwise. The admin
// sign-in this ladder mints is its own credential; nothing is read
// from the environment.

import { rpc } from './lib/rpc.mjs'
import { check, finish } from './lib/check.mjs'
import { state } from './lib/state.mjs'
import { ensureConnection, customMapping } from './lib/admin.mjs'
import { run as preclean } from './steps/preclean.mjs'
import { run as basics } from './steps/basics.mjs'
import { run as guard } from './steps/guard.mjs'
import { run as mapping } from './steps/mapping.mjs'
import { run as cleanup } from './steps/cleanup.mjs'

console.log('0. The admin credential the ladder runs on')
{
  const signIn = await rpc('saka.authn.v1.AuthService/SignIn', {
    identity: 'admin@example.com',
    password: '@dmin123',
  })
  state.admin = signIn.body.access_token ?? ''
  check('0a. the admin signs in', Boolean(state.admin), JSON.stringify(signIn.body).slice(0, 160))
}

console.log('0b. Pre-clean: the rows a previous ladder run left behind')
await preclean()

// The custom connections the ladder walks through — one per mock
// issuerId, discovery-resolved over the nginx TLS front.
await ensureConnection('mock-google', 'google')
await ensureConnection('mock-github', 'github')
await ensureConnection('mock-custom', 'custom', customMapping, [{ key: 'department', claim: 'dept' }])
const connections = await rpc('saka.authn.v1.OAuthSSOService/ListConnections', {}, state.admin)
state.mapped = (connections.body.connections ?? []).find((c) => c.provider === 'mock-custom')

await basics()
await guard()
await mapping()
await cleanup()

finish()
