// The debug ladder's webServer: the run boots its own compiler and its own
// binary, so one invocation proves the ladder with nothing else running.
// The Vite dev compiler must listen where the debug build's proxy looks —
// 127.0.0.1:5173 is web/static_debug.go's build constant, not
// configuration — which is why the compiler cannot share the E2E port with
// the binary. SKIP_GO_BUILD keeps the golang plugin from spawning its own
// `saka serve` on :3000 beside the compiler; the binary this script boots
// below is the run's server. Both children die with the script: Playwright
// signals the webServer command, and the signal is forwarded.
import { execSync, spawn } from 'node:child_process'
import { resolve } from 'node:path'

const projectRoot = resolve(import.meta.dirname, '..', '..')
const buildDir = resolve(projectRoot, 'build', 'debug')
const compilerURL = 'http://127.0.0.1:5173'
const e2ePort = Number(process.env.E2E_PORT || 4473)
const serverURL = `http://localhost:${e2ePort}`

const children = []
let stopping = false

function stop(signal) {
  stopping = true
  for (const child of children) child.kill(signal)
}

process.on('SIGTERM', () => stop('SIGTERM'))
process.on('SIGINT', () => stop('SIGINT'))

// The binary proxies its module graph to the compiler; a request answered
// before the compiler is up is a 502 in the browser, so the server boots
// only once the compiler answers. Any answer counts: the compiler's `/`
// is a 404 by design — the HTML document is the Go shell's, the compiler
// only serves the module graph — so readiness is "it answered", not "it
// answered 200".
async function waitFor(url, budgetMs = 60_000) {
  const deadline = Date.now() + budgetMs
  while (Date.now() < deadline) {
    try {
      await fetch(url)
      return
    } catch {
      // The compiler is not up yet; poll until the budget runs out.
    }
    await new Promise((wake) => setTimeout(wake, 500))
  }
  throw new Error(`the Vite compiler never answered on ${url}`)
}

const compiler = spawn('pnpm', ['exec', 'vp', 'run', 'webapp#dev'], {
  cwd: projectRoot,
  env: { ...process.env, SKIP_GO_BUILD: '1' },
  stdio: 'inherit'
})
children.push(compiler)
compiler.on('exit', (code) => {
  if (!stopping) process.exit(code ?? 1)
})

await waitFor(compilerURL)

execSync('node packages/e2e-tests/make-config.mjs', {
  cwd: projectRoot,
  env: { ...process.env, E2E_BUILD_TAG: 'debug' },
  stdio: 'inherit'
})
execSync(`go build -tags debug -o ${buildDir}/saka ./cmd`, {
  cwd: projectRoot,
  stdio: 'inherit'
})

const server = spawn(
  resolve(buildDir, 'saka'),
  [
    `--config-file=${resolve(buildDir, 'app.config.e2e.json')}`,
    '--env-file=.env.local',
    'serve',
    `--base-url=${serverURL}`,
    `--port=${e2ePort}`
  ],
  { cwd: projectRoot, stdio: 'inherit' }
)
children.push(server)
server.on('exit', (code) => {
  if (!stopping) process.exit(code ?? 1)
})

// Hold the script open for as long as the server runs; Playwright decides
// when the run — and the webServer — is over.
await new Promise(() => {})
