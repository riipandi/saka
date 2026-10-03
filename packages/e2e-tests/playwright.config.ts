import { defineConfig, type Project } from '@playwright/test'
import { resolve } from 'node:path'

// The passkey ladder E2E: a real Chromium against the debug build's devtool
// simulation (/debug/passkey/*), the ceremonies answered by the virtual
// WebAuthn authenticator (context.credentials). The origin is direct
// localhost — a secure context, so the virtual authenticator works without
// the nginx TLS proxy; the proxy stays for the manual cross-device probe.
//
// The runner is Playwright's own; the integration with the vitest suite is
// the npm scripts (e2e*) and the vitest projects' exclude list.

// The server the tests drive: the debug build is the one that carries the
// /debug/passkey pages, and the base-url flag binding is where the RP
// identity derives from.
const serverBaseURL = 'http://localhost:3080'
// Paths are relative to this package: the config lives in
// packages/e2e-tests and the runner's cwd is the package, while the Go
// module and the build output stay at the repository root.
const serverCommand = [
  'go build -tags debug -o ../../build/debug/saka ../../cmd',
  `../../build/debug/saka --env-file=.env.local serve --base-url=${serverBaseURL}`
].join(' && ')

// The projects the e2e-* scripts select. Plain `pnpm e2e` runs the one
// browser the e2e-install installed; the others need their browsers
// installed before their script runs.
const chromiumOnly = (all: Project[]): Project[] => all.slice(0, 1)

const projects: Project[] = [
  { name: 'Chromium', use: { browserName: 'chromium' as const } },
  { name: 'Firefox', use: { browserName: 'firefox' as const } },
  { name: 'Safari', use: { browserName: 'webkit' as const } },
  {
    name: 'Mobile Chrome',
    use: {
      browserName: 'chromium' as const,
      viewport: { width: 390, height: 844 }
    }
  }
]

export default defineConfig({
  testDir: resolve('workflow'),
  outputDir: resolve('.output/e2e-tests'),
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  reporter: process.env.GITHUB_ACTIONS === 'true' ? [['github-actions']] : [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL || serverBaseURL,
    ignoreHTTPSErrors: true,
    trace: 'retain-on-failure'
  },
  projects: process.env.E2E_ALL_BROWSERS ? projects : chromiumOnly(projects),
  webServer: {
    command: serverCommand,
    url: `${serverBaseURL}/api/healthz`,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000
  }
})
