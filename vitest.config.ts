import { defineConfig } from 'vite-plus'

/**
 * One project per live test surface. Surfaces without tests carry no
 * project block — a project is added when its first test lands, not
 * pre-wired against directories that do not exist (the former `api-client`
 * and `app` projects pointed at code that was never written).
 */
export default defineConfig({
  test: {
    reporters:
      process.env.GITHUB_ACTIONS === 'true'
        ? [['github-actions']]
        : [['default'], ['json', { outputFile: './.output/tests-results/vitest-results.json' }]],
    projects: [
      {
        extends: true,
        resolve: { tsconfigPaths: true },
        test: {
          name: 'plugins',
          environment: 'node',
          include: ['./packages/plugins/**/*.test.ts']
        }
      },
      {
        extends: true,
        resolve: { tsconfigPaths: true },
        test: {
          name: 'email',
          environment: 'node',
          include: ['./packages/email/**/*.test.ts']
        }
      }
    ]
  }
})
