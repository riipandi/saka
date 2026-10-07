import { defineConfig } from 'vite-plus'

const ignoredPatterns = [
  '.output',
  '.storage',
  '.tanstack',
  '**/*.md',
  '**/*.mdx',
  '**/*.yml',
  '**/*.yaml',
  '**/*.toml',
  '**/*.tmpl',
  '**/e2e-result/**',
  '**/public/**',
  '/codegen/**',
  '/storage/**',
  '/temp/**'
]

/**
 * Monorepo setup, and nothing else: the repo-wide toolchain blocks every
 * workspace shares (staged commit checks, formatter, linter, cached run
 * tasks). No app pipeline lives here — each frontend package owns its own
 * `vite.config.ts` (`packages/webapp`: the SPA and the Go binary targets;
 * `packages/email`: builds its templates with its own script, no vite
 * config), so a new frontend package is one directory with its own
 * config, not another root clause.
 *
 * The Taskfile owns the sequencing that crosses packages (`task build`
 * compiles the email templates before the webapp pass so the Go binary
 * embeds fresh templates) and the up-to-date checks vp does not cover
 * (`rpc:generate` carries sources/generates). `vp dev` / `vp build` from
 * the workspace root intentionally refuse to pick: enter through
 * `task dev` / `task build`, or target a package with
 * `vp -C packages/webapp <command>`.
 *
 * The test block gathers the workspace's unit tests; `vp test` from the
 * root runs them all. One project per live test surface — a project is
 * added when its first test lands, not pre-wired against directories
 * that do not exist.
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
  },
  staged: {
    '*.{ts,tsx,js,jsx,css,json}': 'vp check --fix',
    '*.go': 'gofmt -w'
  },
  fmt: {
    endOfLine: 'lf',
    useTabs: false,
    tabWidth: 2,
    printWidth: 100,
    insertFinalNewline: true,
    jsxSingleQuote: true,
    singleQuote: true,
    semi: false,
    bracketSpacing: true,
    trailingComma: 'none',
    overrides: [
      {
        files: ['*.json', '*.jsonc'],
        options: { tabWidth: 4 }
      }
    ],
    sortImports: {
      order: 'asc',
      groups: [['builtin', 'external'], ['internal'], ['parent', 'sibling', 'index']],
      internalPattern: ['#/', '~/codegen/'],
      partitionByComment: false,
      partitionByNewline: false,
      newlinesBetween: false,
      ignoreCase: true
    },
    sortPackageJson: {
      sortScripts: false
    },
    ignorePatterns: ignoredPatterns
  },
  lint: {
    jsPlugins: [{ name: 'vite-plus', specifier: 'vite-plus/oxlint-plugin' }],
    options: { typeAware: true, typeCheck: true },
    env: { browser: true, builtin: true, vitest: true },
    categories: { correctness: 'error', suspicious: 'error' },
    rules: {
      'import/default': 'warn',
      'import/no-absolute-path': 'allow',
      'import/no-cycle': 'warn',
      'import/no-unassigned-import': 'off',
      'jsx-a11y/heading-has-content': 'off',
      'no-console': 'off',
      'no-debugger': 'warn',
      'no-empty': ['error', { allowEmptyCatch: true }],
      'no-unused-vars': ['error', { args: 'after-used' }],
      'react/jsx-key': 'error',
      'react/no-array-index-key': 'error',
      'react/no-children-prop': 'off',
      'react/react-in-jsx-scope': 'off',
      'react/self-closing-comp': ['error', { html: true, component: true }],
      'sort-imports': 'off',
      'typescript/consistent-type-definitions': ['error', 'interface'],
      'typescript/no-explicit-any': 'error',
      'typescript/no-floating-promises': 'error',
      'typescript/no-misused-promises': 'error',
      'typescript/no-unnecessary-type-constraint': 'off',
      'typescript/triple-slash-reference': 'allow',
      'vite-plus/prefer-vite-plus-imports': 'error'
    },
    overrides: [
      {
        files: ['**/*.test.ts', '**/*.spec.ts'],
        plugins: ['vitest'],
        rules: {
          'no-console': 'off',
          'vitest/no-focused-tests': 'error',
          'vitest/no-disabled-tests': 'warn'
        }
      },
      {
        files: ['./packages/**/components/**/*.tsx'],
        rules: {
          'jsx-a11y/anchor-has-content': 'off',
          'jsx-a11y/click-events-have-key-events': 'off',
          'jsx-a11y/control-has-associated-label': 'off',
          'jsx-a11y/heading-has-content': 'off',
          'jsx-a11y/label-has-associated-control': 'off',
          'jsx-a11y/no-noninteractive-element-interactions': 'off',
          'jsx-a11y/prefer-tag-over-role': 'off',
          'react/no-array-index-key': 'off'
        }
      }
    ],
    ignorePatterns: ignoredPatterns
  },
  run: {
    cache: { tasks: true },
    tasks: {
      typecheck:
        'pnpm exec tsc -p packages/webapp --noEmit && pnpm exec tsc -p packages/plugins --noEmit && ' +
        'pnpm exec tsc -p packages/email --noEmit && pnpm exec tsc -p packages/e2e-tests --noEmit && ' +
        'pnpm exec tsc -p . --noEmit'
    }
  }
})
