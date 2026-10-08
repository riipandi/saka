import { storybookTest } from '@storybook/addon-vitest/vitest-plugin'
import stylex from '@stylexjs/unplugin/vite'
import react from '@vitejs/plugin-react'
import { createServer } from 'node:http'
import { resolve } from 'node:path'
import { defineConfig, type Plugin, type ViteDevServer } from 'vite-plus'
import { playwright } from 'vite-plus/test/browser-playwright'

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

// The Storybook vitest project's plugin; awaited once — the config factory
// re-runs on every mode change.
const storybookProject = await storybookTest({
  configDir: resolve(import.meta.dirname, '.storybook')
})

// The library's pipeline: StyleX compiles in place (source-sharing, plan D2)
// and the storybook vitest project tests the stories that live in this
// package. Consumers compile the same sources through their own pipelines.
export default defineConfig(({ mode }) => ({
  plugins: [
    vitestStylexCleanup(),
    react({ compiler: true }),
    stylex({
      aliases: { '#/*': resolve(import.meta.dirname, 'src/*') },
      enableDevClassNames: mode === 'development',
      useCSSLayers: { before: ['reset'], prefix: 'stylex' },
      test: Boolean(process.env.VITEST)
    })
  ],
  envPrefix: ['VITE_', 'PUBLIC_'],
  test: {
    projects: [
      {
        // The .storybook/main.ts viteFinal owns this project's plugins,
        // so it does not extend this pipeline.
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
  resolve: { tsconfigPaths: true }
}))
