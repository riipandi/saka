import react from '@vitejs/plugin-react'
import { resolve } from 'node:path'
import { comlink } from 'vite-plugin-comlink'
import { defineConfig } from 'vite-plus'
import golang from '../plugins/plugin-golang.ts'

// Version stamps shared by every Go target; release adds its static-link flags.
const goModule = 'github.com/riipandi/saka'
const repoRoot = resolve(import.meta.dirname, '../..')
const goVersionLdflags = [
  `-X ${goModule}/internal/config.AppVersion=${process.env.BUILD_VERSION || '0.0.0'}`,
  `-X ${goModule}/internal/config.BuildHash=${process.env.BUILD_HASH || 'dev'}`,
  `-X ${goModule}/internal/config.BuildDate=${process.env.BUILD_DATE || new Date().toISOString()}`
]

/**
 * The web application's own pipeline — the SPA sources and the Go binary
 * that serves them are one app: `vite build` compiles the bundle into the
 * Go embed directory (`web/output`), then the go plugin closes the pass by
 * compiling both binaries. `vp dev` runs the same pipeline as the compiler
 * behind the Go port — the browser talks to :3080 only, and the Go debug
 * build proxies the module graph and the HMR socket to this server.
 *
 * The email templates are a separate package with its own build script; the
 * Taskfile sequences the passes (email first, so the binary embeds freshly
 * compiled templates). Template editing in dev happens in the React Email
 * UI (`task email:dev`), which has its own watcher.
 *
 * Plugin order is the contract: comlink owns worker construction and must
 * register first (only plugins that transform ComlinkWorker call sites may
 * precede it); the go plugin's closeBundle derives `assets.json` from the
 * Vite manifest before the binaries compile (the go:embed pattern skips
 * dot directories, so `.vite/` never ships).
 *
 * Every path this config names is absolute (`import.meta.dirname`-based):
 * a package script / `vp -C` run starts in this directory, and the go
 * plugin's watcher, build, and binary spawn all anchor at the repo root
 * via its `root` option.
 *
 * Backend integration (the Vite guide's): the Go binary owns the HTML
 * document — web/shell.go renders it — so there is no index.html. In
 * production the shell resolves every tag from the build manifest, one
 * entry per page; the keys are webapp-root-relative (`src/main.tsx` —
 * web/shell.go names the same key). The Vite-internal manifest lives
 * under `.vite/`, a dot directory go:embed silently skips — deliberate;
 * the Go fragment reads the derived copy (`assets.json`) the
 * embed-manifest plugin writes after every build.
 */
export default defineConfig({
  plugins: [
    comlink(),
    // React Compiler (native oxc path, requires `oxc-transform-react`).
    // Defaults: compilationMode 'infer', panicThreshold 'none' (components
    // that violate the Rules of React are skipped, never broken), target 19.
    react({ compiler: true }),
    golang({
      packageName: 'saka',
      root: repoRoot,
      packagePath: resolve(repoRoot, 'cmd'),
      binArgs: ['--env-file=.env.local', 'serve'],
      build: {
        embedDir: resolve(repoRoot, 'web/output'),
        devTarget: 'debug',
        targets: {
          debug: {
            outputDir: resolve(repoRoot, 'build/debug'),
            buildTags: ['debug', 'noasm', 'nounsafe'],
            ldflags: goVersionLdflags
          },
          release: {
            outputDir: resolve(repoRoot, 'build/release'),
            buildTags: ['release', 'noasm', 'nounsafe'],
            buildFlags: ['-trimpath', '-buildmode=pie', '-buildvcs=false'],
            ldflags: [...goVersionLdflags, '-w -s -extldflags -static']
          }
        }
      }
    })
  ],
  resolve: { tsconfigPaths: true },
  root: resolve(import.meta.dirname),
  publicDir: resolve(import.meta.dirname, 'public'),
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
  // The compiler's port: bound to IPv4 loopback on purpose — the Go debug
  // proxy (web/static_debug.go, viteDevServer) targets 127.0.0.1, and a
  // bare `localhost` bind lands on ::1, which the proxy cannot reach.
  // No proxy block here — the proxy is the Go debug build's side
  // (web/static_debug.go forwards :3080 paths to this server).
  server: { port: 5173, host: '127.0.0.1', strictPort: true }
})
