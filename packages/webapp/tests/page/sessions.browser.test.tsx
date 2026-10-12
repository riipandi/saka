import { create } from '@bufbuild/protobuf'
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect'
import { describe, expect, it, vi } from 'vite-plus/test'
import { SessionsView as SessionsPage } from '#/routes/(app)/account/-sessions-view'
import {
  ListSessionsResponseSchema,
  type ListSessionsResponse,
  type RevokeSessionRequest,
  type RevokeSessionResponse,
  RevokeSessionResponseSchema,
  SessionSchema,
  SessionService,
  type SignOutAllSessionsResponse,
  SignOutAllSessionsResponseSchema,
  type SignOutOtherSessionsResponse,
  SignOutOtherSessionsResponseSchema,
  type Session
} from '~/codegen/authn_pb'
import {
  BeginLoginResponseSchema,
  type ReauthenticateRequest,
  type ReauthenticateResponse,
  ReauthenticateResponseSchema,
  SendReauthenticationCodeResponseSchema,
  WebAuthnService
} from '~/codegen/webauthn_pb'
import { renderView, signedInAuth } from './view-harness'

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
  reauthenticate?: (request: ReauthenticateRequest) => ReauthenticateResponse
  revokeSession?: (request: RevokeSessionRequest, requestHeader: Headers) => RevokeSessionResponse
  onPageChange?: (next: number) => void
}

async function renderSessions(handlers?: SessionHandlers) {
  const reauthenticateHandler =
    handlers?.reauthenticate ??
    (() => create(ReauthenticateResponseSchema, { token: 'proof_from_modal' }))
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
        (() => create(SignOutAllSessionsResponseSchema, { revokedCount: 3, status: 'success' })),
      revokeSession: (request, context) =>
        handlers?.revokeSession
          ? handlers.revokeSession(request, context.requestHeader)
          : create(RevokeSessionResponseSchema, {
              status: context.requestHeader.get('x-saka-reauthentication') ? 'success' : 'refused'
            })
    })
    service(WebAuthnService, {
      reauthenticate: (request) => reauthenticateHandler(request),
      beginLogin: () =>
        create(BeginLoginResponseSchema, {
          options: '{}',
          sessionId: 'wcs_test'
        }),
      sendReauthenticationCode: () => create(SendReauthenticationCodeResponseSchema, {})
    })
  })

  const logout = vi.fn()
  const screen = await renderView(<SessionsPage page={1} onPageChange={handlers?.onPageChange} />, {
    transport,
    auth: signedInAuth({ logout })
  })
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

  it('offers the per-row revoke on the live rows that are not the caller’s own', async () => {
    const { screen } = await renderSessions()
    await expect.element(screen.getByText('Phone Browser')).toBeVisible()
    const revokeButtons = screen.getByRole('button', { name: 'Revoke' }).elements()
    expect(revokeButtons).toHaveLength(1)
  })

  it('revokes through the step-up: the proof is minted by the modal and spent on the call', async () => {
    const spentHeaders: string[] = []
    const revokedIds: string[] = []
    const { screen } = await renderSessions({
      reauthenticate: () => create(ReauthenticateResponseSchema, { token: 'proof_9' }),
      revokeSession: (request, requestHeader) => {
        spentHeaders.push(requestHeader.get('x-saka-reauthentication') ?? '')
        revokedIds.push(request.id)
        return create(RevokeSessionResponseSchema, { status: 'success' })
      }
    })
    await expect.element(screen.getByText('Phone Browser')).toBeVisible()
    const revokeButton = screen.getByRole('button', { name: 'Revoke' })
    await expect.element(revokeButton).toBeVisible()
    await revokeButton.click()
    const dialog = screen.getByRole('alertdialog')
    await expect.element(dialog).toBeVisible()
    await dialog.getByLabelText('Account password').fill('correct-horse-battery')
    const confirm = dialog.getByRole('button', { name: 'Confirm' }).element()
    if (!(confirm instanceof HTMLElement)) throw new Error('the confirm button is not an element')
    confirm.click()
    await vi.waitFor(() => {
      expect(revokedIds).toEqual(['sess_other'])
      expect(spentHeaders).toEqual(['proof_9'])
    })
  })

  it('reopens the challenge when the spent proof answers its refusal', async () => {
    let spent = 0
    const { screen } = await renderSessions({
      revokeSession: () => {
        spent += 1
        if (spent === 1) throw new ConnectError('spent', Code.Unauthenticated)
        return create(RevokeSessionResponseSchema, { status: 'success' })
      }
    })
    await expect.element(screen.getByText('Phone Browser')).toBeVisible()
    await screen.getByRole('button', { name: 'Revoke' }).click()
    const dialog = screen.getByRole('alertdialog')
    await dialog.getByLabelText('Account password').fill('correct-horse-battery')
    const confirm = dialog.getByRole('button', { name: 'Confirm' }).element()
    if (!(confirm instanceof HTMLElement)) throw new Error('the confirm button is not an element')
    confirm.click()
    // The first proof is spent between mint and spend; the dialog returns
    // for the second proof and the revoke lands on it.
    await vi.waitFor(() => {
      expect(spent).toBe(1)
    })
    await expect.element(screen.getByRole('alertdialog')).toBeVisible()
    const secondDialog = screen.getByRole('alertdialog')
    await secondDialog.getByLabelText('Account password').fill('correct-horse-battery')
    const confirmAgain = secondDialog.getByRole('button', { name: 'Confirm' }).element()
    if (!(confirmAgain instanceof HTMLElement)) {
      throw new Error('the confirm button is not an element')
    }
    confirmAgain.click()
    await vi.waitFor(() => {
      expect(spent).toBe(2)
    })
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
    const screen = await renderView(<SessionsPage page={1} onPageChange={onPageChange} />, {
      transport
    })

    // The pager is the numbered window the summary implies; the next button
    // carries the turn to page 2.
    await expect.element(screen.getByRole('button', { name: '2' })).toBeVisible()
    const next = screen.getByRole('button', { name: 'Go to next page' })
    await expect.element(next).not.toHaveAttribute('aria-disabled', 'true')
    await next.click()
    expect(onPageChange).toHaveBeenCalledWith(2)
  })
})
