import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import type { Plugin } from 'vite'

/**
 * Copies the build manifest the Go fragment reads into the binary.
 *
 * Vite writes its manifest under `.vite/` in the output directory — a dot
 * directory `go:embed` (without the `all:` prefix) skips whole, together
 * with everything inside it. The Go side (`web/vite_fragment.go`) instead
 * opens `assets.json`, this plugin's derived copy written beside the
 * assets after every build, so every pipeline that compiles the frontend
 * (task build, goreleaser, Docker, CI) feeds the embed from one source.
 *
 * Both writers of `web/output` run it — the root pipeline and the webapp
 * package's standalone build — so the copy always exists and is never
 * stale: it is written by the same build that wrote the manifest.
 */
export default function VitePluginEmbedManifest(): Plugin {
  return {
    name: 'plugin-embed-manifest',
    closeBundle() {
      const out = resolve(import.meta.dirname, '../../web/output')
      const internal = resolve(out, '.vite/manifest.json')
      if (!existsSync(internal)) return
      writeFileSync(resolve(out, 'assets.json'), readFileSync(internal))
    }
  }
}
