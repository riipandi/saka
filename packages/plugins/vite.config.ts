import { defineConfig } from 'vite-plus'

// The plugins package's vitest project. The root config references this
// file as a project; the include is package-relative on purpose.
export default defineConfig({
  test: {
    projects: [
      {
        extends: true,
        resolve: { tsconfigPaths: true },
        test: {
          name: 'plugins',
          environment: 'node',
          include: ['./**/*.test.ts']
        }
      }
    ]
  }
})
