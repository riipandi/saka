import type { StorybookConfig } from '@storybook/tanstack-react'
import { resolve } from 'node:path'
import { stylexPlugin } from 'plugins/plugin-stylex'
import remarkGfm from 'remark-gfm'
import { mergeConfig } from 'vite'

export default {
  stories: ['./stories/**/*.mdx', '../src/**/*.stories.@(mdx|jsx|tsx)'],
  addons: [
    '@storybook/addon-a11y',
    {
      name: '@storybook/addon-docs',
      options: {
        mdxPluginOptions: {
          mdxCompileOptions: {
            remarkPlugins: [remarkGfm]
          }
        }
      }
    },
    '@storybook/addon-links',
    '@storybook/addon-vitest',
    '@github-ui/storybook-addon-performance-panel',
    '@storybook/addon-mcp'
  ],
  framework: '@storybook/tanstack-react',
  core: {
    allowedHosts: true,
    disableTelemetry: true,
    disableWhatsNewNotifications: true,
    enableCrashReports: false
  },
  features: { backgrounds: false },
  typescript: { reactDocgen: 'react-docgen' },
  async viteFinal(viteConfig) {
    return mergeConfig(viteConfig, {
      resolve: { tsconfigPaths: true },
      build: { chunkSizeWarningLimit: 1024 * 4 },
      server: undefined,
      plugins: [
        stylexPlugin({
          aliases: { '#/*': resolve(import.meta.dirname, '../src/*') }
        })
      ]
    })
  }
} satisfies StorybookConfig
