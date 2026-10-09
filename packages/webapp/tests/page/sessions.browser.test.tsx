import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { AuthContext } from '#/hooks/use-auth'
import { SessionsView as SessionsPage } from '#/routes/(app)/account/-sessions-view'
import {
  ListSessionsResponseSchema,
  type ListSessionsResponse,
  SessionSchema,
  SessionService,
  type SignOutAllSessionsResponse,
  SignOutAllSessionsResponseSchema,
  type SignOutOtherSessionsResponse,
  SignOutOtherSessionsResponseSchema,
  type Session
} from '~/codegen/authn_pb'

type SessionOverrides = Partial<Omit<Session, '$typeName' | '$unknown'>>

function session(id: string, overrides: SessionOverrides = {}): Session {
  return create(SessionSchema, {
    id,
    provider: 'password',
    remember: false,
    createdAt: '2026-10-01T08:00:00Z',
    expiresAt: '2026-10-08T08:00:00Z',
    ...overrides
  })
}

const SESSIONS = [
  session('sess_current', { current: true, userAgent: 'This Browser' }),
  session('sess_other', { userAgent: 'Phone Browser', ipAddress: '10.0.0.9' }),
  session('sess_ended', { revokedAt: '2026-10-02T08:00:00Z', userAgent: 'Old Browser' })
]

interface SessionHandlers {
  listSessions?: () => ListSessionsResponse
  signOutOtherSessions?: () => SignOutOtherSessionsResponse
  signOutAllSessions?: () => SignOutAllSessionsResponse
  onPageChange?: (next: number) => void
}

function wrapperWith(transport: ReturnType<typeof createRouterTransport>, children: ReactNode) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <QueryClientProvider client={queryClient}>
      <TransportProvider transport={transport}>
        <AuthContext.Provider
          value={{
            user: null,
            loggedIn: true,
            isLoading: false,
            login: async () => {},
            continueSignIn: async () => {},
            logout: () => {}
          }}
        >
          {children}
        </AuthContext.Provider>
      </TransportProvider>
    </QueryClientProvider>
  )
}

async function renderSessions(handlers?: SessionHandlers) {
  const transport = createRouterTransport(({ service }) => {
    service(SessionService, {
      listSessions:
        handlers?.listSessions ??
        (() =>
          create(ListSessionsResponseSchema, {
            sessions: SESSIONS,
            metadata: { page: 1, limit: 20, totalPages: 1, totalItems: 3 }
          })),
      signOutOtherSessions:
        handlers?.signOutOtherSessions ??
        (() => create(SignOutOtherSessionsResponseSchema, { revokedCount: 1, status: 'success' })),
      signOutAllSessions:
        handlers?.signOutAllSessions ??
        (() => create(SignOutAllSessionsResponseSchema, { revokedCount: 3, status: 'success' }))
    })
  })

  const logout = vi.fn()
  const screen = await render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <TransportProvider transport={transport}>
        <AuthContext.Provider
          value={{
            user: null,
            loggedIn: true,
            isLoading: false,
            login: async () => {},
            continueSignIn: async () => {},
            logout
          }}
        >
          <SessionsPage page={1} onPageChange={handlers?.onPageChange} />
        </AuthContext.Provider>
      </TransportProvider>
    </QueryClientProvider>
  )
  return { screen, logout }
}

describe('Sessions (browser)', () => {
  it('lists the sessions, the caller’s own marked, ended ones included', async () => {
    const { screen } = await renderSessions()
    await expect.element(screen.getByText('This device')).toBeVisible()
    await expect.element(screen.getByText('Phone Browser')).toBeVisible()
    await expect.element(screen.getByText('Old Browser')).toBeVisible()
    await expect.element(screen.getByText('Ended')).toBeVisible()
  })

  it('carries no per-row revoke — the action waits for the Wave 2 step-up modal', async () => {
    const { screen } = await renderSessions()
    expect(screen.getByRole('button', { name: 'Revoke' }).elements()).toHaveLength(0)
  })

  it('signs out everywhere and drops the pair client-side too', async () => {
    const signOutAllSessions = vi.fn(() =>
      create(SignOutAllSessionsResponseSchema, { revokedCount: 3, status: 'success' })
    )
    const { screen, logout } = await renderSessions({ signOutAllSessions })
    await screen.getByRole('button', { name: 'Sign out all' }).click()
    // The dialog's backdrop intercepts synthetic pointer events; the DOM
    // click is what a real activation reduces to here.
    const confirm = screen
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Sign out all' })
      .element()
    if (!(confirm instanceof HTMLElement)) throw new Error('the confirm button is not an element')
    confirm.click()
    await vi.waitFor(() => {
      expect(signOutAllSessions).toHaveBeenCalled()
      expect(logout).toHaveBeenCalled()
    })
  })

  it('rides the pager the summary answers', async () => {
    const onPageChange = vi.fn()
    const transport = createRouterTransport(({ service }) => {
      service(SessionService, {
        listSessions: () =>
          create(ListSessionsResponseSchema, {
            sessions: SESSIONS,
            metadata: {
              page: 1,
              limit: 20,
              totalPages: 2,
              totalItems: 41,
              firstItemIndex: 20,
              lastItemIndex: 40
            }
          })
      })
    })
    const screen = await render(
      wrapperWith(transport, <SessionsPage page={1} onPageChange={onPageChange} />)
    )

    await expect.element(screen.getByText('21–40 of 41')).toBeVisible()
    const next = screen.getByRole('button', { name: 'Next' })
    await expect.element(next).toBeEnabled()
    await next.click()
    expect(onPageChange).toHaveBeenCalledWith(2)
  })
})
