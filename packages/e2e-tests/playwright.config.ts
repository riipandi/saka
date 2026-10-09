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

// The server the tests drive. The command runs from the repository root —
// the binary requires app.config.json and .env.local beside it, so a run
// from inside the package would never boot its own server — and rebuilds
// the SPA first, because the binary embeds web/output as it stands.
// The build tag is release: the session flows test the surface a real
// deployment serves. The passkey ladder is the exception — its devtool
// pages (/debug/passkey/*) exist only in the debug build, so run it with
// E2E_BUILD_TAG=debug.
const serverBaseURL = 'http://localhost:3000'
const buildTag = process.env.E2E_BUILD_TAG || 'release'
const serverCommand = [
  'pnpm exec vp run webapp#prebuild',
  'pnpm exec vp run webapp#build',
  `go build -tags ${buildTag} -o build/${buildTag}/saka ./cmd`,
  `./build/${buildTag}/saka --env-file=.env.local serve --base-url=${serverBaseURL}`
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
    cwd: resolve('..', '..'),
    reuseExistingServer: !process.env.CI,
    timeout: 120_000
  }
})
