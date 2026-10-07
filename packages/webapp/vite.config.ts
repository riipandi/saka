import react from '@vitejs/plugin-react'
import { resolve } from 'node:path'
import { defineConfig, type UserConfig } from 'vite'
import { comlink } from 'vite-plugin-comlink'
import embedManifest from '../plugins/plugin-manifest.ts'

/**
 * The single definition of the SPA build contract. The root vite.config.ts
 * composes this object instead of duplicating it: it adds the email and
 * golang plugins around this plugin list (email's closeBundle must run
 * before golang's) and inherits root, publicDir, build, and server as they
 * are — so both pipelines agree on the manifest keys (`src/main.tsx`,
 * root-relative — web/shell.go names the same key), the `web/output`
 * directory, and the loopback dev port by construction, not by two hand-
 * kept copies.
 *
 * The standalone build (`vp -C packages/webapp build`) runs this config
 * alone and writes the same bundle + assets.json into the Go embed.
 *
 * Backend integration (unchanged): the Go binary owns the HTML document —
 * web/shell.go renders it — so there is no index.html here. The dev server
 * is the compiler behind the Go port, not an origin of its own: the
 * browser talks to :3080 only, and the Go debug build proxies the module
 * graph and the HMR socket here.
 */
export const webappViteConfig = {
  plugins: [
    comlink(),
    embedManifest(),
    // React Compiler (native oxc path, requires `oxc-transform-react`).
    // Defaults: compilationMode 'infer', panicThreshold 'none' (components
    // that violate the Rules of React are skipped, never broken), target 19.
    react({ compiler: true })
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
} satisfies UserConfig

export default defineConfig(webappViteConfig)
