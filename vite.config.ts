import { resolve } from 'node:path'
import { defineConfig } from 'vite-plus'
import pkg from './package.json' with { type: 'json' }
import email from './packages/plugins/plugin-email.ts'
import golang from './packages/plugins/plugin-golang.ts'
// The SPA build contract lives in the webapp package and is composed here,
// never duplicated — see packages/webapp/vite.config.ts.
import { webappViteConfig } from './packages/webapp/vite.config.ts'

const APP_VERSION = process.env.BUILD_VERSION || pkg.version
const BUILD_DATE = process.env.BUILD_DATE || new Date().toISOString()
const BUILD_HASH = process.env.BUILD_HASH || 'dev'

// Must match the module path in go.mod.
const goModule = 'github.com/riipandi/saka'

// Version stamps shared by every Go target; release adds its static-link flags.
const goVersionLdflags = [
  `-X ${goModule}/internal/config.AppVersion=${APP_VERSION}`,
  `-X ${goModule}/internal/config.BuildHash=${BUILD_HASH}`,
  `-X ${goModule}/internal/config.BuildDate=${BUILD_DATE}`
]

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
  '/codegen/**',
  '/public/**',
  '/storage/**',
  '/temp/**',
  '/packages/webapp/public/**',
  '/packages/e2e-tests/e2e-result/**'
]

/**
 * One binary pipeline, composed: the webapp package owns the SPA build
 * contract (manifest keys, `web/output`, the loopback dev port); this root
 * config owns the repo-wide toolchain blocks (staged, fmt, lint, run tasks)
 * and closes the pass — the email plugin compiles the React Email templates
 * into the Go embed, then the go plugin compiles both binaries around the
 * composed SPA plugin list.
 *
 * Plugin order here is the contract: the webapp list starts with comlink
 * (it owns worker construction and must register first), email's
 * closeBundle must precede golang's so the binary always embeds freshly
 * compiled templates (web/embed.go: email/*.tmpl).
 *
 * Backend integration (the Vite guide's): the Go binary owns the HTML
 * document — web/shell.go renders it — so there is no index.html. The dev
 * server is the compiler behind the Go port, not an origin of its own: the
 * browser talks to :3080 only, the shell's fragment carries same-origin
 * paths, and the debug build proxies the module graph and the HMR socket
 * to this server. In production the shell resolves every tag from the
 * build manifest, one entry per page.
 *
 * The manifest lives under `.vite/`, a dot directory go:embed silently
 * skips — deliberate: the Vite-internal manifest never ships in the
 * binary. The Go fragment reads the derived copy (`assets.json`) the
 * shared `VitePluginEmbedManifest` plugin writes after every build, so
 * every pipeline that compiles the frontend (task build, goreleaser,
 * Docker, CI) feeds the embed from one source.
 */
export default defineConfig({
  ...webappViteConfig,
  // Restated literally so vp's static package detection sees the root
  // package as an app — a spread hides the key from it (bare `vp build` at
  // the workspace root would refuse to pick a target). The value must stay
  // identical to the composed base.
  root: resolve('packages/webapp'),
  // The root config IS the binary pipeline (root: packages/webapp via the
  // composed base), so `vp build` / `vp dev` from the workspace root act on
  // it directly; `vp -C packages/webapp build` runs the standalone SPA pass.
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
      internalPattern: ['#/'],
      partitionByComment: false,
      partitionByNewline: false,
      newlinesBetween: false,
      ignoreCase: true
    },
    ignorePatterns: ignoredPatterns
  },
  lint: {
    options: { typeAware: true, typeCheck: true },
    rules: {
      'typescript/no-floating-promises': 'error',
      'typescript/no-misused-promises': 'error'
    },
    ignorePatterns: ignoredPatterns
  },
  run: {
    cache: true,
    tasks: {
      // Each package checks against its own tsconfig — the root config
      // covers only the root-level TS (see tsconfig.json), so aliases
      // (`#/`, `~/codegen`) resolve where they belong and one package's
      // drift can never satisfy another's contract.
      typecheck:
        'pnpm exec tsc -p packages/webapp --noEmit && pnpm exec tsc -p packages/plugins --noEmit && ' +
        'pnpm exec tsc -p packages/email --noEmit && pnpm exec tsc -p packages/e2e-tests --noEmit && ' +
        'pnpm exec tsc -p . --noEmit'
    }
  },
  plugins: [
    // ...webappViteConfig.plugins → comlink, embed-manifest, react.
    ...webappViteConfig.plugins,
    email({
      templateDir: resolve('packages/email/templates'),
      outputDir: resolve('web/email')
    }),
    golang({
      packageName: pkg.name,
      packagePath: resolve('cmd'),
      binArgs: ['--env-file=.env.local', 'serve'],
      build: {
        embedDir: resolve('web/output'),
        devTarget: 'debug',
        targets: {
          debug: {
            outputDir: resolve('build/debug'),
            buildTags: ['debug', 'noasm', 'nounsafe'],
            ldflags: goVersionLdflags
          },
          release: {
            outputDir: resolve('build/release'),
            buildTags: ['release', 'noasm', 'nounsafe'],
            buildFlags: ['-trimpath', '-buildmode=pie', '-buildvcs=false'],
            ldflags: [...goVersionLdflags, '-w -s -extldflags -static']
          }
        }
      }
    })
  ]
})
