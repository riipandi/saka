import stylex from '@stylexjs/unplugin/vite'
import { devtools } from '@tanstack/devtools-vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { createServer } from 'node:http'
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

// vite preview does not redirect `/storybook` to `/storybook/` (Netlify's
// Pretty URLs do), so only the preview server needs this.
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
// httpServer 'close'. Vitest's server has no httpServer, so the interval
// keeps the process alive — this fake provides the close signal.
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
        devServer.httpServer = createServer()
      }
    },
    buildEnd: closeHttpServer,
    closeWatcher: closeHttpServer
  }
}

/**
 * The app's one vite pipeline: `vp build` compiles the SPA into the Go
 * embed (`web/output`), then the go plugin derives `assets.json` from the
 * manifest and compiles both binaries. `vp dev` runs the same pipeline
 * behind the Go proxy on :3000 — the browser holds one origin, the Go
 * binary owns the HTML document (web/shell.go), and manifest keys are
 * webapp-root-relative. Email templates are a separate package the
 * Taskfile builds first. Paths are `import.meta.dirname`-absolute so a
 * package script or `vp -C` run works from anywhere.
 */
export default defineConfig(({ mode }) => ({
  plugins: [
    // Must precede stylex: the HMR-interval cleanup registers on
    // server.httpServer, absent in vitest's middleware-mode server.
    vitestStylexCleanup(),
    comlink(),
    stylex({
      aliases: { '#/*': resolve(import.meta.dirname, 'src/*') },
      enableDevClassNames: mode === 'development',
      useCSSLayers: { before: ['reset'], prefix: 'stylex' },
      // Vitest compiles in StyleX's test mode: debug class names, no CSS.
      test: Boolean(process.env.VITEST)
    }),
    // Dev-only plugins stay out of vitest's server; golang no-ops under
    // VITEST itself. react must follow tanstackRouter — vite-plus enforces it.
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
      }
    ]
  },
  resolve: { tsconfigPaths: true },
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
