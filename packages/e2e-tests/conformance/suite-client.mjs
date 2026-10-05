// The conformance suite's HTTP client. The suite's front serves a private
// certificate chain (its own prebuilt nginx), so the requests ride a
// node:https agent that does not verify the peer — a local rehearsal talks
// to a container the developer started, not a remote service. Every call
// is the JSON the suite's REST API documents: the plan create, the runner
// create, the info poll, and the log read.

import https from 'node:https'
import { URL } from 'node:url'

const agent = new https.Agent({ rejectUnauthorized: false })

// request performs one JSON call against the suite. The body and the
// answer are JSON documents; a non-2xx answer carries the suite's error
// body, which the caller reads off the thrown error.
export function request(base, method, path, { query, body } = {}) {
  const url = new URL(base + path)
  for (const [key, value] of Object.entries(query ?? {})) {
    url.searchParams.set(key, value)
  }
  const payload = body === undefined ? undefined : JSON.stringify(body)

  return new Promise((resolve, reject) => {
    const req = https.request(
      url,
      {
        method,
        agent,
        headers: {
          'Content-Type': 'application/json',
          Accept: 'application/json',
          ...(payload === undefined ? {} : { 'Content-Length': Buffer.byteLength(payload) })
        }
      },
      (res) => {
        const chunks = []
        res.on('data', (chunk) => chunks.push(chunk))
        res.on('end', () => {
          const text = Buffer.concat(chunks).toString('utf8')
          let parsed = text
          try {
            parsed = text === '' ? {} : JSON.parse(text)
          } catch {
            // A non-JSON answer is the error page shape; keep the text.
          }
          if (res.statusCode >= 400) {
            reject(
              new Error(
                `${method} ${url.pathname} answered ${res.statusCode}: ${text.slice(0, 400)}`
              )
            )
            return
          }
          resolve(parsed)
        })
      }
    )
    req.on('error', reject)
    if (payload !== undefined) req.write(payload)
    req.end()
  })
}

// createPlan creates one test plan: the name and the variant select the
// profile, the configuration body carries the alias, the issuer's
// discovery URL, and the clients. The suite answers the plan with its id
// and the module list the runner walks.
export function createPlan(base, planName, variant, configuration) {
  return request(base, 'POST', '/api/plan', {
    query: { planName, ...(variant ? { variant: JSON.stringify(variant) } : {}) },
    body: configuration
  })
}

// createTest starts one module of a plan. The module's variant rides the
// query as Spring binds maps — `variant[key]=value` per entry — the shape
// the suite's own runner scripts send; a JSON body here converts to
// nothing the module lookup matches.
export function createTest(base, planId, moduleName, variant) {
  const query = { test: moduleName, plan: planId }
  for (const [key, value] of Object.entries(variant ?? {})) {
    query[`variant[${key}]`] = value
  }
  return request(base, 'POST', '/api/runner', { query })
}

// testStatus is the runner payload: the browser URLs the operator must
// visit ride `browser.urls`.
export function testStatus(base, testId) {
  return request(base, 'GET', `/api/runner/${testId}`)
}

// testInfo is the status/result document the runner polls.
export function testInfo(base, testId) {
  return request(base, 'GET', `/api/info/${testId}`)
}

// testLog is the module's log entries — the runner reads it to name a
// failure in the report.
export function testLog(base, testId) {
  return request(base, 'GET', `/api/log/${testId}`)
}

// health answers when the suite is up; the runner waits for it before the
// first plan create. The static front answers the SPA for everything that
// is not an API path, so the probe reads an API endpoint.
export function health(base) {
  return request(base, 'GET', '/api/runner/available')
}
