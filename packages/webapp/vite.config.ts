import react from '@vitejs/plugin-react'
import { resolve } from 'node:path'
import { defineConfig } from 'vite'
import { comlink } from 'vite-plugin-comlink'
import embedManifest from '../plugins/plugin-manifest.ts'

/**
 * The webapp package's own pipeline: the SPA bundle into the Go embed
 * directory. The root vite.config.ts composes this pipeline with the email
 * templates and the golang binary targets, and its vite root points at
 * this package — so both builds produce the SAME manifest keys (root
 * relative: `src/main.tsx`), and web/output has exactly one content
 * contract no matter which entry ran it.
 *
 * Backend integration (unchanged): the Go binary owns the HTML document —
 * web/shell.go renders it — so there is no index.html here. The dev
 * server is the compiler behind the Go port, not an origin of its own:
 * the browser talks to :3080 only, and the Go debug build proxies the
 * module graph and the HMR socket here.
 */
export default defineConfig({
  plugins: [
    comlink(),
    embedManifest(),
    // React Compiler (native oxc path, requires `oxc-transform-react`).
    // Defaults: compilationMode 'infer', panicThreshold 'none' (components
    // that violate the Rules of React are skipped, never broken), target 19.
    // Storybook compiles via its own framework plugins — uncompiled reference.
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
})
