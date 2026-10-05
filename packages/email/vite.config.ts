import { resolve } from 'node:path'
import { defineConfig } from 'vite'
import email from '../plugins/plugin-email.ts'

/**
 * The email package's own pipeline: `vp build packages/email` compiles the
 * React Email templates into the Go templates the binary embeds
 * (web/email/*.tmpl — the go:embed contract web/embed.go reads). The root
 * vite.config.ts composes the same plugin for the production binary build,
 * so both entries must name the same template dir and output dir. The
 * bundler needs one entry of its own — the templates render in-process,
 * they are never bundled — hence the empty module below.
 */
export default defineConfig({
  plugins: [
    email({
      templateDir: resolve(import.meta.dirname, 'templates'),
      outputDir: resolve(import.meta.dirname, '../../web/email')
    })
  ],
  root: resolve(import.meta.dirname),
  build: {
    emptyOutDir: true,
    outDir: resolve(import.meta.dirname, '../../.output/email'),
    rolldownOptions: {
      input: { email: resolve(import.meta.dirname, 'templates/index.ts') }
    }
  }
})
