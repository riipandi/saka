// The load test's configuration: every knob reads the environment once, so a
// run's shape is named on the command line and never edited in code.

export const BASE_URL = __ENV.K6_TARGET || 'http://localhost:3080'

// The account the load test signs in as. The seeded administrator covers the
// admin-gated reads (audit log, queue admin); override for a narrower one.
export const IDENTITY = __ENV.K6_IDENTITY || 'admin@example.com'
export const PASSWORD = __ENV.K6_PASSWORD || '@dmin123'

// The storage fixture, uploaded by setup() when the engine is live. A key
// that already exists is left alone — the fixture is idempotent by key.
export const STORAGE_BUCKET = __ENV.K6_STORAGE_BUCKET || 'k6-loadtest'
export const STORAGE_KEY = __ENV.K6_STORAGE_KEY || 'loadtest/ping.bin'
export const STORAGE_BODY = 'k6 storage fixture — the bytes only need to be stable.\n'

// Profile selection: `bench:loadtest K6_PROFILE=load` or `K6_PROFILE=stress`.
// smoke is a correctness pass (one VU, short); load is the working shape of a
// busy instance; stress ramps until the surface answers differently than it
// does at the working shape — the breakpoint, not a target to live at.
export const PROFILE = __ENV.K6_PROFILE || 'smoke'

// Optional overrides; the profile's own shape fills what they leave out.
const VUS = Number(__ENV.K6_VUS || 0) || 50
const DURATION = __ENV.K6_DURATION || ''

export const profiles = {
  smoke: {
    executor: 'constant-vus',
    vus: 1,
    duration: DURATION || '20s'
  },
  load: {
    executor: 'ramping-vus',
    startVUs: 0,
    stages: [
      { duration: DURATION || '1m', target: VUS },
      { duration: DURATION || '3m', target: VUS },
      { duration: DURATION || '30s', target: 0 }
    ],
    gracefulRampDown: '20s'
  },
  stress: {
    executor: 'ramping-arrival-rate',
    startRate: 10,
    timeUnit: '1s',
    preAllocatedVUs: Math.max(VUS, 200),
    maxVUs: Math.max(VUS * 4, 800),
    stages: [
      { duration: DURATION || '2m', target: 100 },
      { duration: DURATION || '3m', target: 100 },
      { duration: DURATION || '2m', target: 300 },
      { duration: DURATION || '3m', target: 300 },
      { duration: DURATION || '1m', target: 0 }
    ]
  }
}

// The refresh scenario is serial by contract — its token pair rotates per
// call, so a second concurrent refresh is a reuse the session revokes. Every
// profile holds it to one VU regardless of the others' shape.
export const serialScenarios = new Set(['refresh'])

// Thresholds read the per-request `name` tag, so each endpoint answers for
// itself. A scenario that made no requests (the storage fixture skipped)
// passes vacuously — k6 thresholds over no samples are met.
export function thresholds() {
  const t = {}
  const add = (name, dur, failRate) => {
    t[`http_req_duration{name:${name}}`] = [
      { threshold: `p(95)<${dur}`, abortOnFail: false },
      { threshold: 'p(99)<1000', abortOnFail: false }
    ]
    t[`http_req_failed{name:${name}}`] = [{ threshold: `rate<${failRate}`, abortOnFail: false }]
  }
  for (const name of ['healthz', 'jwks', 'storage-get']) add(name, 200, 0.01)
  for (const name of [
    'get-session',
    'list-sessions',
    'notification-list',
    'auditlog-list',
    'admin-queues'
  ]) {
    add(name, 300, 0.01)
  }
  // The password hasher is expensive by design; the budget reflects a
  // deliberate scrypt, not a slow query. 429s from the rate limiter are
  // expected at stress rates and count against this failure rate, so its
  // budget is looser on purpose.
  add('signin', 1500, 0.05)
  add('refresh', 500, 0.01)
  return t
}
