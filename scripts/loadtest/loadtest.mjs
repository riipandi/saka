// The k6 load test for the Saka HTTP surface. Choose a shape with
// K6_PROFILE=smoke|load|stress (default smoke); the nine crucial endpoints
// run as tagged scenarios, so the end-of-run summary answers each endpoint
// for itself.
//
//   k6 run scripts/loadtest/loadtest.mjs
//   K6_PROFILE=load K6_TARGET=http://localhost:3000 k6 run scripts/loadtest/loadtest.mjs
//
// Prerequisites: a running server (task dev or a built release binary), a
// seeded database (task db:migrate && task db:seed) whose account matches
// K6_IDENTITY/K6_PASSWORD, and — for the storage scenario — the storage
// engine live (driver local); setup() stages its own fixture and the
// scenario skips with a count when the engine is not answering.

import { check, sleep } from 'k6'
import { PROFILE, serialScenarios, STORAGE_BUCKET, STORAGE_KEY, thresholds } from './lib/config.mjs'
import { fixtureSkipped, setupStorage } from './lib/fixture.mjs'
import { callRPC, get, procedures, signIn } from './lib/rpc.mjs'

// The scenario names — the exec functions below, listed so the profiles can
// map over them.
const scenarioNames = [
  'healthz',
  'jwks',
  'signin',
  'get-session',
  'list-sessions',
  'refresh',
  'notification-list',
  'auditlog-list',
  'admin-queues',
  'storage-get'
]

// K6_ONLY narrows the run to one scenario (comma-separated for several), so a
// single endpoint can be probed without editing the script.
const only = (__ENV.K6_ONLY || '')
  .split(',')
  .map((s) => s.trim())
  .filter(Boolean)

function shape(profile, name, spec) {
  // The exec name is a JavaScript export — camelCase; the dashed name is
  // what the tag, the threshold, and the summary read. The refresh scenario
  // is serial in every profile: its token pair rotates per call, and a
  // concurrent refresh is a reuse the session revokes.
  const exec = name.replace(/-([a-z])/g, (_, c) => c.toUpperCase())
  if (serialScenarios.has(name)) {
    return { executor: 'constant-vus', vus: 1, duration: '20s', exec }
  }
  return { ...spec, exec }
}

function smokeScenarios() {
  // Iterations, not duration: a one-VU duration executor loops flat out and
  // the run would measure the rate limiter refusing it. Twenty honest
  // iterations per scenario is the correctness pass.
  const spec = { executor: 'per-vu-iterations', vus: 1, iterations: 20, maxDuration: '1m' }
  const names = only.length ? scenarioNames.filter((n) => only.includes(n)) : scenarioNames
  return Object.fromEntries(names.map((name) => [name, shape('smoke', name, spec)]))
}

export const options = {
  scenarios:
    PROFILE === 'stress'
      ? loadScenarios('stress')
      : PROFILE === 'load'
        ? loadScenarios('load')
        : smokeScenarios(),
  thresholds: thresholds(),
  discardResponseBodies: false,
  insecureSkipTLSVerify: true,
  noConnectionReuse: false,
  userAgent: 'k6-loadtest (saka bench)'
}

const stages = (target) => [
  { duration: '1m', target },
  { duration: '3m', target },
  { duration: '30s', target: 0 }
]

function loadScenarios(profile) {
  // The load profile spreads the virtual users across the surface by
  // importance; the stress profile arrives by rate instead, so the run
  // finds the ceiling rather than holding a shape.
  if (profile === 'load') {
    const spec = { executor: 'ramping-vus', startVUs: 0, gracefulRampDown: '20s' }
    const weights = {
      healthz: 8,
      jwks: 8,
      'storage-get': 8,
      'get-session': 12,
      'list-sessions': 4,
      'notification-list': 4,
      'auditlog-list': 2,
      'admin-queues': 2,
      signin: 2
    }
    const scenarios = Object.fromEntries(
      Object.entries(weights)
        .filter(([name]) => !only.length || only.includes(name))
        .map(([name, weight]) => [
          name,
          shape(profile, name, { ...spec, stages: stages(Math.ceil(50 / 12) * weight) })
        ])
    )
    // The serial refresh rides every profile — its rotations are the write
    // path the reads above lean on.
    if (!only.length || only.includes('refresh')) {
      scenarios.refresh = { executor: 'constant-vus', vus: 1, duration: '4m30s', exec: 'refresh' }
    }
    return scenarios
  }
  // stress: arrival rates per scenario, ramped — the shape that finds
  // where the surface bends.
  const spec = {
    executor: 'ramping-arrival-rate',
    timeUnit: '1s',
    preAllocatedVUs: 50
  }
  const rates = {
    healthz: [20, 120],
    jwks: [20, 120],
    'storage-get': [10, 80],
    'get-session': [30, 200],
    'list-sessions': [8, 50],
    'notification-list': [8, 50],
    'auditlog-list': [4, 25],
    'admin-queues': [2, 15],
    signin: [1, 8]
  }
  const scenarios = Object.fromEntries(
    Object.entries(rates)
      .filter(([name]) => !only.length || only.includes(name))
      .map(([name, [from, to]]) => [
        name,
        shape(profile, name, {
          ...spec,
          startRate: from,
          stages: [
            { duration: '2m', target: Math.round(to / 2) },
            { duration: '3m', target: Math.round(to / 2) },
            { duration: '2m', target: to },
            { duration: '3m', target: to },
            { duration: '1m', target: 0 }
          ]
        })
      ])
  )
  // The serial refresh rides every profile — one VU rotating its own pair,
  // paced by the exec's sleep, for the length of the run.
  if (!only.length || only.includes('refresh')) {
    scenarios.refresh = { executor: 'constant-vus', vus: 1, duration: '11m', exec: 'refresh' }
  }
  return scenarios
}

// ---------------------------------------------------------------------------
// setup / teardown
// ---------------------------------------------------------------------------

let refreshPair = { accessToken: '', refreshToken: '' }

export function setup() {
  const primary = signIn()
  if (!primary.ok) {
    throw new Error(
      `setup: the load test account did not sign in (${primary.code}) — seed the database and match K6_IDENTITY/K6_PASSWORD`
    )
  }

  // The refresh scenario holds its own pair, so its rotations never spend
  // the pair the other scenarios read.
  refreshPair = signIn()
  if (!refreshPair.ok) {
    refreshPair = primary
  }

  const fixture = setupStorage(primary.accessToken)
  return {
    accessToken: primary.accessToken,
    refreshToken: primary.refreshToken,
    storage: fixture
  }
}

// ---------------------------------------------------------------------------
// scenarios — one exec per crucial endpoint; the `name` tag carries the
// scenario into the thresholds and the summary.
// ---------------------------------------------------------------------------

export function healthz() {
  // The checker's own endpoint — there is no root /healthz route; the SPA's
  // not-found shell answers that path, which is not a health answer.
  const res = get('healthz', '/api/healthz')
  check(res, { 'healthz is 200': (r) => r.status === 200 })
}

export function jwks() {
  const res = get('jwks', '/.well-known/jwks.json')
  check(res, { 'jwks is 200': (r) => r.status === 200 })
  check(res, { 'jwks names keys': (r) => r.status === 200 && r.json('keys') !== undefined })
}

export function signin() {
  const out = signIn()
  check(out, { 'signin answers the pair': (o) => o.ok && o.accessToken !== '' })
}

export function getSession(data) {
  const out = callRPC('get-session', procedures.getSession, {}, data.accessToken)
  check(out, { 'get-session names the session': (o) => o.ok && (o.data.session?.id ?? '') !== '' })
}

export function listSessions(data) {
  const out = callRPC('list-sessions', procedures.listSessions, {}, data.accessToken)
  check(out, { 'list-sessions is success': (o) => o.ok })
}

export function refresh(data) {
  // One rotation per pass, paced: the pair rotates in place, so this
  // scenario is the only writer of its own session.
  const out = callRPC('refresh', procedures.refresh, { refresh_token: data.refreshToken }, '')
  if (out.ok && out.data.refresh_token) {
    data.refreshToken = out.data.refresh_token
  }
  check(out, { 'refresh answers a pair': (o) => o.ok && (o.data.access_token ?? '') !== '' })
  sleep(2)
}

export function notificationList(data) {
  const out = callRPC(
    'notification-list',
    procedures.listNotifications,
    { page: 1, limit: 20 },
    data.accessToken
  )
  check(out, { 'notification list is success': (o) => o.ok })
}

export function auditlogList(data) {
  const out = callRPC(
    'auditlog-list',
    procedures.auditlogList,
    { page: 1, limit: 20 },
    data.accessToken
  )
  check(out, { 'auditlog list is success': (o) => o.ok })
}

export function adminQueues(data) {
  const queues = callRPC('admin-queues', procedures.listQueues, {}, data.accessToken)
  check(queues, { 'list-queues is success': (o) => o.ok })
  if (!queues.ok) {
    return
  }
  const first = queues.data.queues?.[0]?.name ?? ''
  if (first === '') {
    return
  }
  const tasks = callRPC(
    'admin-queues',
    procedures.listTasks,
    { queue: first, page: 1, limit: 20 },
    data.accessToken
  )
  check(tasks, { 'list-tasks is success': (o) => o.ok })
}

export function storageGet(data) {
  if (!data.storage?.available) {
    fixtureSkipped.add(1)
    return
  }
  const res = get('storage-get', `/storage/${STORAGE_BUCKET}/${STORAGE_KEY}`)
  check(res, { 'storage serves the object': (r) => r.status === 200 })
}

export function teardown() {
  // Nothing to tear down: the fixture object is left in place (its key is
  // stable, so the next run re-serves it), and the sessions the runs
  // opened age out under the session policy.
}
