import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { APP_CONFIG_QUERY_KEY } from '#/hooks/use-app-config'
import { shutdownTelemetry, telemetryActive } from '#/libraries/telemetry/telemetry'
import { TelemetryProvider } from '#/libraries/telemetry/telemetry-provider'

/**
 * The mount is the composition under test: the provider sits in the root
 * stack beside the other providers, so a tracer that breaks there breaks
 * every page. The browser here is real — the idle tick, the config read
 * from the query cache, and the init all run for actual. The document is
 * primed into the cache the boot prefetch warms, so the suite never dials
 * the deployment.
 */

const deployment = {
  app: { mode: 'development', base_url: 'http://localhost:3000', timezone: 'UTC' },
  otel: { browser: { endpoint: 'http://localhost:4318', ratio: 0 } }
}

function clientWith(config: unknown): QueryClient {
  const queryClient = new QueryClient()
  queryClient.setQueryData(APP_CONFIG_QUERY_KEY, config)
  return queryClient
}

function wrapper(queryClient: QueryClient): (props: { children: ReactNode }) => ReactNode {
  return ({ children }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

beforeEach(async () => {
  await shutdownTelemetry()
})

afterEach(async () => {
  await shutdownTelemetry()
})

describe('the telemetry provider mount (browser)', () => {
  it('stays inert when the deployment publishes no collector', async () => {
    const screen = await render(<TelemetryProvider />, {
      wrapper: wrapper(clientWith({ ...deployment, otel: { browser: { endpoint: '', ratio: 0 } } }))
    })
    await expect.element(screen.baseElement).toBeInTheDocument()
    expect(telemetryActive()).toBe(false)
  })

  it('starts the tracer once the document names a collector', async () => {
    const screen = await render(<TelemetryProvider />, { wrapper: wrapper(clientWith(deployment)) })
    await expect.element(screen.baseElement).toBeInTheDocument()
    await expect.poll(async () => telemetryActive(), { timeout: 5_000 }).toBe(true)
  })
})
