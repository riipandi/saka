import http from 'node:http'
import type { Plugin, ViteDevServer } from 'vite'

/** Where the Go server the document comes from listens. */
export interface DevShellOptions {
  /** Origin of the Go server serving the shell (default "http://127.0.0.1:3080"). */
  target?: string
}

/**
 * Serves the Go-rendered shell through the Vite dev server, so both origins
 * of the dev loop display the same application: the Go port renders the
 * document natively, and this port answers a navigation by fetching the
 * shell from Go while Vite keeps serving the modules, the styles, and HMR.
 *
 * Only document navigations forward — a request whose Accept names
 * text/html. Everything else (modules, the HMR socket, the API proxies)
 * stays with Vite, and an asset request is never turned into a document.
 * The plugin speaks plain node:http because it runs before Vite's own
 * middlewares and must not recurse into them.
 */
export default function devShellPlugin(userOptions: DevShellOptions = {}): Plugin {
  const target = userOptions.target ?? 'http://127.0.0.1:3080'

  return {
    name: 'vite-plugin-devshell',
    apply: 'serve',
    configureServer(server: ViteDevServer) {
      server.middlewares.use((req, res, next) => {
        const accept = req.headers.accept ?? ''
        if (req.method !== 'GET' || !accept.includes('text/html')) {
          next()
          return
        }

        const upstream = http.request(
          `${target}${req.url}`,
          { headers: { ...req.headers, accept: accept } },
          (go) => {
            res.writeHead(go.statusCode ?? 502, go.headers)
            go.pipe(res)
          }
        )
        upstream.on('error', () => {
          res.writeHead(502, { 'content-type': 'text/plain' })
          res.end(
            `the Go server at ${target} is not up — start it or wait for the go plugin's build`
          )
        })
        req.pipe(upstream)
      })
    }
  }
}
