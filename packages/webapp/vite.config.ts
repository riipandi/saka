import react from '@vitejs/plugin-react'
import { resolve } from 'node:path'
import golang from 'plugins/plugin-golang'
import { comlink } from 'vite-plugin-comlink'
import { defineConfig } from 'vite-plus'

// Version stamps shared by every Go target; release adds its static-link flags.
const goModule = 'github.com/riipandi/saka'
const repoRoot = resolve(import.meta.dirname, '../..')
const goVersionLdflags = [
  `-X ${goModule}/internal/config.AppVersion=${process.env.BUILD_VERSION || '0.0.0'}`,
  `-X ${goModule}/internal/config.BuildHash=${process.env.BUILD_HASH || 'dev'}`,
  `-X ${goModule}/internal/config.BuildDate=${process.env.BUILD_DATE || new Date().toISOString()}`
]

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
    // Comlink owns worker construction; only plugins that transform
    // ComlinkWorker call sites may precede it.
    comlink(),
    // React Compiler via the native oxc path: components that violate the
    // Rules of React are skipped, never broken.
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
  // IPv4 loopback on purpose: the Go debug proxy targets 127.0.0.1, and a
  // bare `localhost` bind lands on ::1, which the proxy cannot reach.
  server: { port: 5173, host: '127.0.0.1', strictPort: true }
}))
