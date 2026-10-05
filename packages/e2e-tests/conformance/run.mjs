// The conformance rehearsal's runner: the plan create, the module walk,
// the browser hand-offs, and the report. One invocation runs one profile's
// plan end to end and answers non-zero when a module failed.
//
//   pnpm -C packages/e2e-tests exec node conformance/run.mjs <plan> [variant JSON]
//
// The plans the rehearsal runs (the suite's names):
//   oidcc-basic-certification-test-plan
//   oidcc-config-certification-test-plan
//   oidcc-rp-initiated-logout-certification-test-plan
//   oidcc-backchannel-rp-initiated-logout-certification-test-plan
//
// The variant JSON selects the response type and the client registration
// ({"response_type":"code id_token","client_registration":"static_client"}
// shapes); the runner passes it through untouched.

import { signIn } from '../workflow/rpc.mjs'
import { admin, apiBase, buildConfiguration, issuer, suiteBase } from './config.mjs'
import { openDriver } from './driver.mjs'
import { createPlan, createTest, health, testInfo, testLog, testStatus } from './suite-client.mjs'

const planName = process.argv[2]
if (!planName) {
  console.error('usage: node conformance/run.mjs <plan-name> [variant-json]')
  process.exit(2)
}
const variant = process.argv[3] ? JSON.parse(process.argv[3]) : undefined

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

async function main() {
  await health(suiteBase)
  console.log(`suite:   ${suiteBase}`)
  console.log(`issuer:  ${issuer}`)

  const bearer = await signIn(apiBase, admin.user, admin.pass)

  const configuration = buildConfiguration(planName)

  const plan = await createPlan(suiteBase, planName, variant, configuration)
  console.log(`plan:    ${plan.id} (${plan.modules.length} modules)`)

  // A module filter keeps a fix-it loop short: CONFORMANCE_MODULES=name1,name2
  const filter = process.env.CONFORMANCE_MODULES
  const modules = filter
    ? plan.modules.filter((module) =>
        filter.split(',').some((name) => module.testModule.includes(name))
      )
    : plan.modules
  if (modules.length !== plan.modules.length) {
    console.log(`filter:  ${modules.length}/${plan.modules.length} modules`)
  }

  const driver = await openDriver({ bearer, issuerOrigin: issuer })
  const outcomes = []
  try {
    for (const module of modules) {
      const instance = await createTest(suiteBase, plan.id, module.testModule, module.variant)
      const result = await runModule(driver, instance.id)
      outcomes.push({ module: module.testModule, ...result })
      const mark = result.ok ? 'ok   ' : 'FAIL '
      console.log(
        `${mark} ${module.testModule} → ${result.result ?? result.status} ${result.note ?? ''}`
      )
      if (!result.ok) {
        // The failure's own words: the last log entries name the
        // condition the suite stopped on.
        const log = await testLog(suiteBase, instance.id).catch(() => [])
        for (const entry of log.filter((item) => item.result === 'FAILURE').slice(-3)) {
          console.log(`       ${String(entry.msg ?? entry.message ?? '').slice(0, 220)}`)
        }
      }
    }
  } finally {
    await driver.close()
  }

  const failed = outcomes.filter((outcome) => !outcome.ok)
  console.log(`\n==== ${outcomes.length - failed.length}/${outcomes.length} modules passed ====`)
  if (failed.length > 0) {
    for (const outcome of failed)
      console.log(`  - ${outcome.module}: ${outcome.result ?? outcome.status}`)
    process.exit(1)
  }
}

// runModule drives one test instance to a terminal state: the runner
// polls, walks every new browser URL the suite queues, and reads the
// result the suite settles on.
async function runModule(driver, testId) {
  let visited = 0
  let lastStatus = ''
  let idle = 0
  for (let attempt = 0; attempt < 3000; attempt++) {
    const [status, info] = await Promise.all([
      testStatus(suiteBase, testId),
      testInfo(suiteBase, testId)
    ])

    if (['FINISHED', 'INTERRUPTED'].includes(info.status)) {
      const ok = ['PASSED', 'SKIPPED', 'WARNING', 'REVIEW'].includes(info.result)
      return { status: info.status, result: info.result, ok }
    }

    const urls = status.browser?.urls ?? []
    if (urls.length > visited) {
      for (let index = visited; index < urls.length; index++) {
        try {
          const note = await driver.drive(urls[index])
          if (note !== 'left-the-issuer' && note !== 'redirected') {
            console.log(`  note: ${note} for ${urls[index].slice(0, 90)}`)
          }
        } catch (error) {
          console.log(`  note: the browser leg failed: ${String(error).slice(0, 160)}`)
        }
      }
      visited = urls.length
      idle = 0
    } else if (info.status === lastStatus) {
      idle++
    } else {
      idle = 0
    }
    lastStatus = info.status

    // A module that sits without progress is dead — read its last log
    // lines into the note so the report names the failure.
    if (idle > 600) {
      const log = await testLog(suiteBase, testId).catch(() => [])
      const last = log.at(-1)
      return {
        status: info.status,
        result: info.result ?? 'STALLED',
        ok: false,
        note: last?.message ? String(last.message).slice(0, 200) : ''
      }
    }
    await sleep(200)
  }
  return { status: 'TIMEOUT', ok: false }
}

main().catch((error) => {
  console.error(String(error))
  process.exit(1)
})
