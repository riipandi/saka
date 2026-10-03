import { resolve } from 'node:path'
import { defineConfig } from 'vite-plus'
import pkg from './package.json' with { type: 'json' }
import email from './packages/plugins/plugin-email.ts'
import golang from './packages/plugins/plugin-golang.ts'
import embedManifest from './packages/plugins/plugin-manifest.ts'

// const isTestOrCI = process.env.CI || process.env.VITEST
const isStorybook = process.env.STORYBOOK === 'true'
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
 * Plugin Comlink owns worker construction and must register first: only
 * plugins that transform ComlinkWorker call sites may precede it.
 *
 * Plugin Email must be registered before the go plugin: its closeBundle
 * compiles the email templates that web/embed.go pulls into the go binary.
 *
 * With plugin Go, the SPA bundle and the email templates are compiled
 * once and embedded into both binaries.
 *
 * Backend integration (the Vite guide's): the Go binary owns the HTML
 * document — web/shell.go renders it — so there is no index.html here.
 * The dev server is the compiler behind the Go port, not an origin of its
 * own: the browser talks to :3080 only, the shell's fragment carries
 * same-origin paths, and the debug build proxies the module graph and the
 * HMR socket to this server. In production the shell resolves every tag
 * from the build manifest, one entry per page.
 *
 * Monorepo: the SPA sources live in packages/webapp (the package builds
 * the same bundle standalone — this config's vite root points at that
 * package, so both builds agree on the manifest keys), the email templates
 * in packages/email (`vp build packages/email` compiles them alone), the
 * Vite plugins in packages/plugins, and Playwright in packages/e2e-tests.
 * This root config is still the canonical binary pipeline: the email
 * plugin compiles the templates into the Go embed directories, and the go
 * plugin closes the pass by compiling both binaries. The manifest keys
 * are webapp-root-relative — web/shell.go names the same key.
 *
 * The manifest lives under `.vite/`, a dot directory go:embed silently
 * skips — deliberate: the Vite-internal manifest never ships in the
 * binary. The Go fragment reads the derived copy (`assets.json`) the
 * shared `VitePluginEmbedManifest` plugin writes after every build, so every
 * pipeline that compiles the frontend (task build, goreleaser, Docker,
 * CI) feeds the embed from one source.
 */

export default defineConfig({
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
        options: {
          tabWidth: 4
        }
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
    cache: true
  },
  plugins: [
    embedManifest(),
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
  ],
  resolve: { tsconfigPaths: true },
  root: resolve('packages/webapp'),
  publicDir: resolve('packages/webapp/public'),
  build: {
    manifest: true,
    emptyOutDir: true,
    chunkSizeWarningLimit: 1024 * 4,
    outDir: resolve('web/output'),
    reportCompressedSize: false,
    rolldownOptions: {
      input: { app: resolve('packages/webapp/src/main.tsx') }
    }
  },
  // The compiler's port: bound to the loopback, proxied by the Go
  // debug build, and never opened by a developer or a deployment.
  server: isStorybook ? undefined : { port: 5173, host: '127.0.0.1', strictPort: true }
})
