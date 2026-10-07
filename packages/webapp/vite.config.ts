import { storybookTest } from '@storybook/addon-vitest/vitest-plugin'
import stylex from '@stylexjs/unplugin/vite'
import { devtools } from '@tanstack/devtools-vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { EventEmitter } from 'node:events'
import { resolve } from 'node:path'
import golang from 'plugins/plugin-golang'
import { comlink } from 'vite-plugin-comlink'
import { defineConfig } from 'vite-plus'
import type { Plugin, ViteDevServer } from 'vite-plus'
import { playwright } from 'vite-plus/test/browser-playwright'

// Version stamps shared by every Go target; release adds its static-link flags.
const goModule = 'github.com/riipandi/saka'
const projectRoot = resolve(import.meta.dirname, '../..')
const goVersionLdflags = [
  `-X ${goModule}/internal/config.AppVersion=${process.env.BUILD_VERSION || '0.0.0'}`,
  `-X ${goModule}/internal/config.BuildHash=${process.env.BUILD_HASH || 'dev'}`,
  `-X ${goModule}/internal/config.BuildDate=${process.env.BUILD_DATE || new Date().toISOString()}`
]

// vite preview does not redirect directory paths, so `/storybook` would 404
// even though `/storybook/` serves the built Storybook. Netlify handles this
// itself (Pretty URLs), so only the preview server needs it.
function storybookPreviewRedirect(): Plugin {
  return {
    name: 'storybook-preview-redirect',
    configurePreviewServer: (server) => {
      server.middlewares.use((req, res, next) => {
        const url = new URL(req.url ?? '/', 'http://localhost')
        if (url.pathname === '/storybook') {
          res.statusCode = 301
          res.setHeader('Location', `/storybook/${url.search}`)
          return res.end()
        }
        return next()
      })
    }
  }
}

// StyleX starts a dev HMR interval in configureServer and only clears it on
// httpServer 'close'. Vitest's Vite server often has no httpServer, so the
// interval keeps the process alive after tests finish.
function vitestStylexCleanup(): Plugin {
  let server: ViteDevServer | undefined
  const closeHttpServer = () => {
    server?.httpServer?.emit('close')
  }
  return {
    name: 'vitest-stylex-cleanup',
    enforce: 'pre',
    apply: 'serve',
    configureServer(devServer) {
      server = devServer
      if (!devServer.httpServer) {
        // A bare EventEmitter stands in for the httpServer: stylex only needs
        // something to register its interval cleanup on. Deliberate type lie.
        // oxlint-disable-next-line typescript/no-unsafe-type-assertion
        devServer.httpServer = new EventEmitter() as ViteDevServer['httpServer']
      }
    },
    buildEnd: closeHttpServer,
    closeWatcher: closeHttpServer
  }
}

// The Storybook vitest project's plugin indexes the stories from the
// .storybook/main.ts glob; awaited once at module load because the config
// factory re-runs on every mode change.
const storybookProject = await storybookTest({
  configDir: resolve(import.meta.dirname, '.storybook')
})

/**
 * The app's one vite pipeline — the SPA and the Go binary that serves it
 * are one application. `vp build` compiles the bundle into the Go embed
 * (`web/output`), then the go plugin derives `assets.json` from the Vite
 * manifest (the go:embed pattern skips dot directories) and compiles both
 * binaries. `vp dev` runs the same pipeline behind the Go port: the
 * browser talks to :3080 only; the debug build proxies the module graph
 * and the HMR socket here. The Go binary owns the HTML document
 * (web/shell.go), so there is no index.html; manifest keys are
 * webapp-root-relative (`src/main.tsx` — web/shell.go names the same key).
 *
 * The email templates are a separate package with its own build script;
 * the Taskfile sequences the passes (email first). Template editing in
 * dev happens in the React Email UI (`task email:dev`).
 *
 * Paths are `import.meta.dirname`-absolute — a package script or `vp -C`
 * run starts in this directory — and plugin-golang's `root` option anchors
 * its watcher, build, and binary spawn at the repo root.
 */
export default defineConfig(({ mode }) => ({
  plugins: [
    // Must precede stylex: stylex's configureServer registers its HMR
    // interval's cleanup on `server.httpServer` — absent in vitest's
    // middleware-mode server — so the fake httpServer must exist by then.
    vitestStylexCleanup(),
    comlink(),
    stylex({
      aliases: { '#/*': resolve(import.meta.dirname, 'src/*') },
      enableDevClassNames: mode === 'development',
      useCSSLayers: { before: ['reset'], prefix: 'stylex' },
      test: Boolean(process.env.VITEST)
    }),
    // Dev-only plugins: the router generator and the devtools overlay have
    // no business inside vitest's Vite server (the generated routes file is
    // already on disk), and the golang plugin no-ops under VITEST itself.
    // react must stay AFTER tanstackRouter (vite-plus enforces the order),
    // so it sits inside both branches.
    ...(process.env.VITEST
      ? [react({ compiler: true })]
      : [
          devtools(),
          tanstackRouter({
            routesDirectory: resolve(import.meta.dirname, 'src/routes'),
            generatedRouteTree: resolve(import.meta.dirname, 'src/routes.gen.ts'),
            autoCodeSplitting: true,
            target: 'react'
          }),
          react({ compiler: true }),
          golang({
            packageName: 'saka',
            root: projectRoot,
            packagePath: resolve(projectRoot, 'cmd'),
            binArgs: ['--env-file=.env.local', 'serve'],
            build: {
              embedDir: resolve(projectRoot, 'web/output'),
              devTarget: 'debug',
              targets: {
                debug: {
                  outputDir: resolve(projectRoot, 'build/debug'),
                  buildTags: ['debug', 'noasm', 'nounsafe'],
                  ldflags: goVersionLdflags
                },
                release: {
                  outputDir: resolve(projectRoot, 'build/release'),
                  buildTags: ['release', 'noasm', 'nounsafe'],
                  buildFlags: ['-trimpath', '-buildmode=pie', '-buildvcs=false'],
                  ldflags: [...goVersionLdflags, '-w -s -extldflags -static']
                }
              }
            }
          }),
          storybookPreviewRedirect()
        ])
  ],
  envPrefix: ['VITE_', 'PUBLIC_'],
  root: resolve(import.meta.dirname),
  publicDir: resolve(import.meta.dirname, 'public'),
  // The test setup mirrors the vite-react-template the components were
  // ported from: a happy-dom unit project, a real-browser component project
  // (vitest-browser-react over Playwright Chromium), and the Storybook
  // project that renders the stories. End-to-end tests live separately in
  // packages/e2e-tests — Playwright against a running server — so nothing
  // here duplicates them. The go plugin no-ops under VITEST.
  test: {
    projects: [
      {
        extends: true,
        resolve: { tsconfigPaths: true },
        test: {
          name: 'unit',
          environment: 'happy-dom',
          environmentOptions: { happyDOM: { url: 'http://localhost:3000/' } },
          setupFiles: ['./tests/setup-test.ts'],
          include: ['./**/*.{test,spec}.{ts,tsx}'],
          exclude: ['**/node_modules/**', '**/*.browser.{test,spec}.{ts,tsx}'],
          globals: true
        }
      },
      {
        extends: true,
        resolve: { tsconfigPaths: true },
        test: {
          name: 'browser',
          browser: {
            enabled: true,
            headless: true,
            provider: playwright(),
            instances: [{ browser: 'chromium' }]
          },
          setupFiles: ['./tests/setup-browser.ts'],
          include: ['./**/*.browser.{test,spec}.{ts,tsx}'],
          exclude: ['**/node_modules/**'],
          globals: true
        }
      },
      {
        // The .storybook/main.ts viteFinal owns this project's plugins,
        // so it does not extend the app pipeline.
        extends: false,
        plugins: [storybookProject],
        test: {
          name: 'storybook',
          exclude: ['./**/*.{test,spec}.{ts,tsx}', '**/node_modules/**'],
          browser: {
            enabled: true,
            headless: true,
            provider: playwright(),
            instances: [{ browser: 'chromium' }]
          }
        }
      }
    ]
  },
  build: {
    manifest: true,
    emptyOutDir: true,
    chunkSizeWarningLimit: 1024 * 4,
    outDir: resolve(import.meta.dirname, '../../web/output'),
    reportCompressedSize: false,
    rolldownOptions: {
      input: { app: resolve(import.meta.dirname, 'src/main.tsx') }
    }
  },
  worker: { plugins: () => [comlink()] },
  // IPv4 loopback on purpose: the Go debug proxy targets 127.0.0.1, and a
  // bare `localhost` bind lands on ::1, which the proxy cannot reach.
  server: { port: 5173, host: '127.0.0.1', strictPort: true }
}))
