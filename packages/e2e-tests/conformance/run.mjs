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
import {
  createPlan,
  createTest,
  health,
  testInfo,
  testLog,
  testStatus,
  uploadImage,
  waitState
} from './suite-client.mjs'

const planName = process.argv[2]
if (!planName) {
  console.error('usage: node conformance/run.mjs <plan-name> [variant-json]')
  process.exit(2)
}
const variant = process.argv[3] ? JSON.parse(process.argv[3]) : undefined

async function main() {
  await health(suiteBase)
  console.log(`suite:   ${suiteBase}`)
  console.log(`issuer:  ${issuer}`)

  const bearer = await signIn(apiBase, admin.user, admin.pass)

  const configuration = buildConfiguration(planName)

  const plan = await createPlan(suiteBase, planName, variant, configuration)
  console.log(`plan:    ${plan.id} (${plan.modules.length} modules)`)

  // A module filter keeps a fix-it loop short: CONFORMANCE_MODULES=name1,name2 —
  // an exact name matches that module alone; a partial name falls back to
  // the substring match.
  const filter = process.env.CONFORMANCE_MODULES
  const names = (filter ?? '').split(',')
  const exact = plan.modules.filter((module) => names.some((name) => module.testModule === name))
  const modules = filter
    ? exact.length > 0
      ? exact
      : plan.modules.filter((module) => names.some((name) => module.testModule.includes(name)))
    : plan.modules
  if (modules.length !== plan.modules.length) {
    console.log(`filter:  ${modules.length}/${plan.modules.length} modules`)
  }

  const driver = await openDriver({ bearer, issuerOrigin: issuer })
  const outcomes = []
  try {
    for (const module of modules) {
      const instance = await createTest(suiteBase, plan.id, module.testModule, module.variant)
      console.log(`  module ${instance.id}: ${module.testModule}`)
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

// runModule drives one test instance to a terminal state. The wait is the
// suite's own long-poll: each call holds until the status moves or the
// per-call budget elapses, so a module that sits between steps — a
// max_age test waiting its second out — holds the call instead of the
// runner starting the next module under the same alias and tripping the
// suite's one-instance-per-alias rule. Between calls the runner walks
// every browser URL the suite queued.
async function runModule(driver, testId) {
  let visited = 0
  let uploaded = 0
  const deadline = Date.now() + 600_000
  while (Date.now() < deadline) {
    await waitState(suiteBase, testId, 'WAITING,CONFIGURED,FINISHED,INTERRUPTED', 10_000).catch(
      () => {}
    )

    const info = await testInfo(suiteBase, testId)

    if (['FINISHED', 'INTERRUPTED'].includes(info.status)) {
      const ok = ['PASSED', 'SKIPPED', 'WARNING', 'REVIEW'].includes(info.result)
      return { status: info.status, result: info.result, ok }
    }

    if (info.status === 'WAITING') {
      // The suite's summary tells the operator when a test must meet a
      // user agent that holds no OP session — the cookie the completed
      // authorizations left behind has to go.
      if (typeof info.summary === 'string' && info.summary.includes('cookie')) {
        await driver.clearCookies()
      }

      // The image placeholders some modules hold the test for: the
      // screenshot of the browser as it stands is the evidence they want.
      const log = await testLog(suiteBase, testId).catch(() => [])
      const placeholders = log.filter((entry) => 'upload' in entry).map((entry) => entry.upload)
      for (; uploaded < placeholders.length; uploaded++) {
        const image = await driver.screenshot()
        await uploadImage(suiteBase, testId, placeholders[uploaded], image)
      }

      const status = await testStatus(suiteBase, testId)
      const urls = status.browser?.urls ?? []
      for (; visited < urls.length; visited++) {
        try {
          const note = await driver.drive(urls[visited])
          if (note !== 'left-the-issuer' && note !== 'redirected') {
            console.log(`  note: ${note} for ${urls[visited].slice(0, 90)}`)
          }
        } catch (error) {
          console.log(`  note: the browser leg failed: ${String(error).slice(0, 160)}`)
        }
      }
    }
  }
  return { status: 'TIMEOUT', ok: false }
}

main().catch((error) => {
  console.error(String(error))
  process.exit(1)
})
