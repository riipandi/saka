// A one-module probe the fix-it loop reads: create the plan's module, wait,
// and dump the suite's log entries for it.
import { buildConfiguration } from './config.mjs'
import { request } from './suite-client.mjs'

const base = 'https://localhost.emobix.co.uk:8443'
const planId = process.argv[2]
const moduleName = process.argv[3]
const configuration = buildConfiguration(process.argv[4] ?? 'oidcc-basic-certification-test-plan')

const plan = await request(base, 'GET', '/api/plan/' + planId)
const instance = await request(base, 'POST', '/api/runner', {
  query: { test: moduleName, plan: plan.id },
  body: configuration
})
console.log('instance:', instance.id)

await new Promise((r) => setTimeout(r, 20000))

const info = await request(base, 'GET', '/api/info/' + instance.id)
console.log('status:', info.status, 'result:', info.result)
const status = await request(base, 'GET', '/api/runner/' + instance.id)
console.log('browser urls:', JSON.stringify(status.browser?.urls ?? []).slice(0, 400))
const log = await request(base, 'GET', '/api/log/' + instance.id)
for (const entry of log.slice(-12)) {
  console.log('[' + (entry.time ?? '') + ']', String(entry.message ?? '').slice(0, 240))
}
