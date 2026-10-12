import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter
} from '@tanstack/react-router'
import { describe, expect, it, vi } from 'vite-plus/test'
import { type RenderResult } from 'vitest-browser-react'
import { ResetPasswordView } from '#/routes/(auth)/-reset-password-view'
import {
  PasswordRecoveryService,
  type ResetPasswordResponse,
  ResetPasswordResponseSchema
} from '~/codegen/authn_pb'
import { renderView, signedInAuth } from './view-harness'

const OK: ResetPasswordResponse = create(ResetPasswordResponseSchema, {
  status: 'success',
  message: 'the password was reset'
})

interface ResetHandlers {
  resetPassword?: (request: { token: string; newPassword: string }) => ResetPasswordResponse
}

async function renderReset(
  handlers: ResetHandlers = {},
  prefill?: string
): Promise<{ screen: RenderResult }> {
  const transport = createRouterTransport(({ service }) => {
    service(PasswordRecoveryService, {
      resetPassword: (request) =>
        handlers.resetPassword?.(request) ??
        create(ResetPasswordResponseSchema, {
          status: 'success',
          message: 'the password was reset'
        })
    })
  })

  const RootRoute = createRootRoute({
    component: () => <ResetPasswordView prefill={prefill} />
  })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: ['/reset-password'] })
  })
  const screen = await renderView(<RouterProvider router={router} />, {
    transport,
    auth: signedInAuth({ loggedIn: false })
  })
  return { screen }
}

describe('Reset password (browser)', () => {
  it('spends the code on the new credential', async () => {
    const resetPassword = vi.fn<
      (request: { token: string; newPassword: string }) => ResetPasswordResponse
    >(() => OK)
    const { screen } = await renderReset({ resetPassword })
    await screen.getByLabelText('Reset code').fill('a'.repeat(64))
    await screen.getByLabelText('New password').fill('griffin-guard-9')
    await screen.getByLabelText('Confirm it').fill('griffin-guard-9')
    await screen.getByRole('button', { name: 'Reset password' }).click()
    await vi.waitFor(() => {
      expect(resetPassword).toHaveBeenCalledWith(
        expect.objectContaining({
          token: 'a'.repeat(64),
          newPassword: 'griffin-guard-9'
        })
      )
    })
  })

  it('keeps the submit disabled until the form holds a candidate', async () => {
    const { screen } = await renderReset()
    const button = screen.getByRole('button', { name: 'Reset password' })
    await expect.element(button).toBeDisabled()
    await screen.getByLabelText('Reset code').fill('a'.repeat(64))
    await screen.getByLabelText('New password').fill('griffin-guard-9')
    await expect.element(button).toBeDisabled()
    await screen.getByLabelText('Confirm it').fill('different')
    await expect.element(button).toBeDisabled()
    await screen.getByLabelText('Confirm it').fill('griffin-guard-9')
    await expect.element(button).toBeEnabled()
  })

  it('prefills the code the search carried', async () => {
    const { screen } = await renderReset({}, 'b'.repeat(64))
    const code = screen.getByLabelText('Reset code')
    await expect.element(code).toHaveValue('b'.repeat(64))
  })
})
