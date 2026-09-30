import { resolve } from 'node:path'
import { loadEnv } from 'vite'
import { defineConfig } from 'vitest/config'

const excludedPath = ['node_modules', 'tests-e2e', 'e2e-tests']

export default defineConfig({
  test: {
    reporters:
      process.env.GITHUB_ACTIONS === 'true'
        ? [['github-actions']]
        : [
            ['default'],
            ['json', { outputFile: resolve('.output/tests-results/vitest-results.json') }],
            ['html', { outputDir: resolve('.output/tests-results') }]
          ],
    browser: { traceView: true },
    coverage: {
      provider: 'v8',
      reporter: ['html-spa', 'text-summary'],
      reportsDirectory: resolve('.output/tests-results/coverage'),
      include: ['./api/client/**/*.{js,ts}'],
      cleanOnRerun: true,
      clean: true,
      thresholds: {
        global: {
          statements: 80,
          branches: 70,
          functions: 75,
          lines: 80
        }
      }
    },
    projects: [
      {
        extends: true,
        resolve: { tsconfigPaths: true },
        test: {
          name: 'api-client',
          environment: 'node',
          env: loadEnv('test', process.cwd(), ''),
          include: ['./api/client/**/*.test.ts'],
          exclude: excludedPath,
          globals: true
        }
      },
      {
        extends: true,
        resolve: { tsconfigPaths: true },
        test: {
          name: 'app',
          environment: 'happy-dom',
          include: ['./app/**/*.test.ts'],
          exclude: excludedPath,
          globals: true
        }
      },
      {
        extends: true,
        resolve: { tsconfigPaths: true },
        test: {
          name: 'plugins',
          environment: 'node',
          include: ['./plugins/**/*.test.ts'],
          exclude: excludedPath,
          globals: true
        }
      }
    ]
  }
})
