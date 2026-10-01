import { resolve } from 'node:path'
import { defineConfig } from 'vite'
import { comlink } from 'vite-plugin-comlink'
import pkg from './package.json' with { type: 'json' }
import email from './plugins/plugin-email.ts'
import golang from './plugins/plugin-golang.ts'

// const isTestOrCI = process.env.CI || process.env.VITEST
const isStorybook = process.env.STORYBOOK === 'true'
const APP_VERSION = process.env.BUILD_VERSION || pkg.version
const BUILD_DATE = process.env.BUILD_DATE || new Date().toISOString()
const BUILD_HASH = process.env.BUILD_HASH || 'dev'

// Must match the module path in go.mod.
const goModule = 'github.com/riipandi/tango'

// Version stamps shared by every Go target; release adds its static-link flags.
const goVersionLdflags = [
  `-X ${goModule}/internal/config.AppVersion=${APP_VERSION}`,
  `-X ${goModule}/internal/config.BuildHash=${BUILD_HASH}`,
  `-X ${goModule}/internal/config.BuildDate=${BUILD_DATE}`
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
 * document — web/shell.go renders it — so there is no index.html here and
 * no dev proxy either. In development the browser opens the Go port
 * (3080); the shell's fragment points the assets at this dev server, and
 * the API calls stay same-origin against Go, which keeps the session
 * cookie first-party. In production the shell resolves every tag from the
 * build manifest, one entry per page.
 *
 * The root is the repository so the manifest keys name the source paths
 * the Go side knows: app/main.tsx is the application document, and a
 * second page adds its own key to the input map (see web.Page).
 */
export default defineConfig({
  plugins: [
    comlink(),
    email({
      templateDir: resolve('email/templates'),
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
  root: resolve('.'),
  publicDir: resolve('public'),
  build: {
    manifest: true,
    emptyOutDir: true,
    chunkSizeWarningLimit: 1024 * 4,
    outDir: resolve('web/output'),
    reportCompressedSize: false,
    rolldownOptions: {
      input: { app: resolve('app/main.tsx') }
    }
  },
  worker: { plugins: () => [comlink()] },
  server: isStorybook
    ? undefined
    : {
        port: 3000,
        strictPort: true,
        cors: { origin: '*' }
      }
})
