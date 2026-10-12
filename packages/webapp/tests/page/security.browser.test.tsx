import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import { describe, expect, it, vi } from 'vite-plus/test'
import { type RenderResult } from 'vitest-browser-react'
import { SecurityView } from '#/routes/(app)/account/-security-view'
import {
  BeginTotpEnrollmentResponseSchema,
  ConfirmTotpEnrollmentResponseSchema,
  DeleteTotpEnrollmentResponseSchema,
  DisableMfaResponseSchema,
  ListTotpEnrollmentsResponseSchema,
  MultifactorService,
  RegenerateRecoveryCodesResponseSchema,
  TotpEnrollmentSchema,
  type TotpEnrollment
} from '~/codegen/authn_pb'
import {
  AddPasswordResponseSchema,
  GetCurrentUserResponseSchema,
  type GetCurrentUserResponse,
  RemovePasswordResponseSchema,
  UserSchema,
  UserService
} from '~/codegen/identity_pb'
import {
  ReauthenticateResponseSchema,
  SendReauthenticationCodeResponseSchema,
  WebAuthnService,
  ListCredentialsResponseSchema,
  BeginRegistrationResponseSchema,
  UpdateCredentialResponseSchema,
  DeleteCredentialResponseSchema,
  CredentialSchema
} from '~/codegen/webauthn_pb'
import { renderView, signedInAuth } from './view-harness'

const USER = create(UserSchema, {
  id: 'usr_langdon',
  username: 'rlangdon',
  email: 'robert@langdon.dev',
  displayName: 'Robert Langdon',
  passwordUpdatedAt: '2026-09-01T08:00:00Z'
})

const USER_WITHOUT_PASSWORD = create(UserSchema, {
  id: 'usr_neveu',
  username: 'sneveu',
  email: 'sophie@neveu.dev',
  displayName: 'Sophie Neveu'
})

function enrollment(id: string, name: string, confirmed: boolean): TotpEnrollment {
  return create(TotpEnrollmentSchema, {
    totpId: id,
    name,
    createdAt: { seconds: 1790000000n, nanos: 0 },
    confirmedAt: confirmed ? { seconds: 1790000100n, nanos: 0 } : undefined
  })
}

/** What the calls the tests judge carried — the spent proof and the
 * second-factor codes the bodies carried. */
interface Captured {
  proof: string | null
  deleted: { totpId: string; code: string } | null
  regenerated: { code: string } | null
  disabled: { code: string } | null
  added: { newPassword: string } | null
  credentialDeleted: { credentialId: string; proof: string | null } | null
  renamed: { credentialId: string; name: string } | null
  registrationStarted: number
  begun: { name: string } | null
  confirmed: { totpId: string; code: string } | null
}

interface SecurityOptions {
  user?: GetCurrentUserResponse
  enrollments?: TotpEnrollment[]
  passkeys?: TotpEnrollment[]
}

async function renderSecurity(
  options: SecurityOptions = {}
): Promise<{ screen: RenderResult; captured: Captured }> {
  const captured: Captured = {
    proof: null,
    deleted: null,
    regenerated: null,
    disabled: null,
    added: null,
    credentialDeleted: null,
    renamed: null,
    registrationStarted: 0,
    begun: null,
    confirmed: null
  }
  const credential = create(CredentialSchema, {
    id: 'psk_v1',
    name: 'Aegis on tablet',
    createdAt: { seconds: 1790000000n, nanos: 0 }
  })
  const transport = createRouterTransport(({ service }) => {
    service(UserService, {
      getCurrentUser: (): GetCurrentUserResponse =>
        options.user ??
        create(GetCurrentUserResponseSchema, { user: USER, status: 'success', message: 'ok' }),
      addPassword: (request, context) => {
        captured.proof = context.requestHeader.get('x-saka-reauthentication')
        captured.added = { newPassword: request.newPassword }
        return create(AddPasswordResponseSchema, { status: 'success', message: 'ok' })
      },
      removePassword: (_request, context) => {
        captured.proof = context.requestHeader.get('x-saka-reauthentication')
        return create(RemovePasswordResponseSchema, { status: 'success', message: 'ok' })
      }
    })
    service(MultifactorService, {
      listTotpEnrollments: () =>
        create(ListTotpEnrollmentsResponseSchema, {
          enrollments: options.enrollments ?? []
        }),
      deleteTotpEnrollment: (request) => {
        captured.deleted = { totpId: request.totpId, code: request.code }
        return create(DeleteTotpEnrollmentResponseSchema, { message: 'removed' })
      },
      regenerateRecoveryCodes: (request, context) => {
        captured.proof = context.requestHeader.get('x-saka-reauthentication')
        captured.regenerated = { code: request.code }
        return create(RegenerateRecoveryCodesResponseSchema, {
          recoveryCodes: ['3f9a1c02', '8b2e47d1']
        })
      },
      disableMfa: (request, context) => {
        captured.proof = context.requestHeader.get('x-saka-reauthentication')
        captured.disabled = { code: request.code }
        return create(DisableMfaResponseSchema, { message: 'disabled' })
      },
      beginTotpEnrollment: (request) => {
        captured.begun = { name: request.name }
        return create(BeginTotpEnrollmentResponseSchema, {
          totpId: 'totp_repro',
          name: request.name,
          secret: 'MFRGGZDFMZTQ====',
          otpauthUri: 'otpauth://totp/Saka:rlangdon?secret=MFRGGZDFMZTQ===='
        })
      },
      confirmTotpEnrollment: (request) => {
        captured.confirmed = { totpId: request.totpId, code: request.code }
        return create(ConfirmTotpEnrollmentResponseSchema, {
          totpId: request.totpId,
          recoveryCodes: ['3f9a1c02', '8b2e47d1']
        })
      }
    })
    service(WebAuthnService, {
      reauthenticate: () => create(ReauthenticateResponseSchema, { token: 'proof_9' }),
      sendReauthenticationCode: () => create(SendReauthenticationCodeResponseSchema, {}),
      listCredentials: () =>
        create(ListCredentialsResponseSchema, {
          credentials: options.passkeys ? [credential] : []
        }),
      beginRegistration: () => {
        captured.registrationStarted += 1
        return create(BeginRegistrationResponseSchema, {
          options: '{}',
          sessionId: 'wcs_reg'
        })
      },
      updateCredential: (request) => {
        captured.renamed = { credentialId: request.credentialId, name: request.name }
        return create(UpdateCredentialResponseSchema, { credential })
      },
      deleteCredential: (request, context) => {
        captured.credentialDeleted = {
          credentialId: request.credentialId,
          proof: context.requestHeader.get('x-saka-reauthentication')
        }
        return create(DeleteCredentialResponseSchema, {})
      }
    })
  })

  const screen = await renderView(<SecurityView />, {
    transport,
    auth: signedInAuth()
  })
  return { screen, captured }
}

/** Prove through the password factor the step-up modal opens with. */
async function prove(screen: RenderResult) {
  await expect.element(screen.getByRole('alertdialog')).toBeVisible()
  await screen.getByLabelText('Account password').fill('correct-horse-battery')
  const confirm = screen.getByRole('alertdialog').getByRole('button', { name: 'Confirm' }).element()
  if (!(confirm instanceof HTMLElement)) throw new Error('the confirm button is not an element')
  confirm.click()
}

describe('Security (browser)', () => {
  it('renders the password card with the credential’s last-set instant', async () => {
    const { screen } = await renderSecurity()
    await expect.element(screen.getByText(/Last set 1 Sept 2026/)).toBeVisible()
  })

  it('removes the password behind the reauthentication proof', async () => {
    const { screen, captured } = await renderSecurity()
    await screen.getByRole('button', { name: 'Remove password' }).click()
    await prove(screen)
    await vi.waitFor(() => {
      expect(captured.proof).toBe('proof_9')
    })
  })

  it('adds the password behind the proof, the form gating itself', async () => {
    const { screen, captured } = await renderSecurity({
      user: create(GetCurrentUserResponseSchema, {
        user: USER_WITHOUT_PASSWORD,
        status: 'success',
        message: 'ok'
      })
    })
    const button = screen.getByRole('button', { name: 'Set password' })
    await expect.element(button).toBeDisabled()
    await screen.getByLabelText('New password').fill('griffin-guard-9')
    await screen.getByLabelText('Confirm it').fill('griffin-guard-9')
    await button.click()
    await prove(screen)
    await vi.waitFor(() => {
      expect(captured.proof).toBe('proof_9')
      expect(captured.added?.newPassword).toBe('griffin-guard-9')
    })
  })

  it('removes a pending authenticator without any code — the row proves nothing yet', async () => {
    const { screen, captured } = await renderSecurity({
      enrollments: [enrollment('totp_pending', 'Aegis on tablet', false)]
    })
    await expect.element(screen.getByText('Aegis on tablet')).toBeVisible()
    await screen.getByRole('button', { name: 'Remove' }).click()
    await vi.waitFor(() => {
      expect(captured.deleted).toEqual({ totpId: 'totp_pending', code: '' })
    })
  })

  it('enrolls an authenticator in the inline panel: name, scan, code, codes once', async () => {
    const { screen, captured } = await renderSecurity()
    await screen.getByRole('button', { name: 'Add authenticator' }).click()

    const panel = screen.getByTestId('totp-enrollment-panel')
    await expect.element(panel.getByText('Step 1 of 3 — name the device')).toBeVisible()
    await panel.getByLabelText('Name this device').fill('Aegis on tablet')
    await panel.getByRole('button', { name: 'Continue' }).click()
    await vi.waitFor(() => {
      expect(captured.begun).toEqual({ name: 'Aegis on tablet' })
    })

    await expect.element(panel.getByText('Step 2 of 3 — scan and confirm')).toBeVisible()
    await expect.element(panel.getByText('MFRGGZDFMZTQ====')).toBeVisible()
    const slots = panel.getByRole('textbox')
    await expect.element(slots.first()).toBeVisible()
    await slots.first().fill('039471')
    await panel.getByRole('button', { name: 'Confirm' }).click()
    await vi.waitFor(() => {
      expect(captured.confirmed).toEqual({ totpId: 'totp_repro', code: '039471' })
    })

    await expect.element(panel.getByText('Step 3 of 3 — save the recovery codes')).toBeVisible()
    await expect.element(panel.getByText('3f9a1c02')).toBeVisible()
    await panel.getByRole('button', { name: 'I saved my recovery codes' }).click()
    await expect.element(screen.getByTestId('totp-enrollment-panel')).not.toBeInTheDocument()
  })

  it('abandons a begun enrollment on cancel — the unconfirmed row is a mistyped start', async () => {
    const { screen, captured } = await renderSecurity()
    await screen.getByRole('button', { name: 'Add authenticator' }).click()

    const panel = screen.getByTestId('totp-enrollment-panel')
    await panel.getByLabelText('Name this device').fill('Aegis on tablet')
    await panel.getByRole('button', { name: 'Continue' }).click()
    await expect.element(panel.getByText('Step 2 of 3 — scan and confirm')).toBeVisible()

    await panel.getByRole('button', { name: 'Cancel' }).click()
    await vi.waitFor(() => {
      expect(captured.deleted).toEqual({ totpId: 'totp_repro', code: '' })
    })
    await expect.element(screen.getByTestId('totp-enrollment-panel')).not.toBeInTheDocument()
  })

  it('removes a confirmed authenticator while another stands — the proof is not asked', async () => {
    const { screen, captured } = await renderSecurity({
      enrollments: [
        enrollment('totp_a', 'Aegis on tablet', true),
        enrollment('totp_b', 'iPhone 17', true)
      ]
    })
    await expect.element(screen.getByText('iPhone 17')).toBeVisible()
    // Two rows carry the action; the second row's button is the one the
    // scenario aims at.
    await screen.getByRole('button', { name: 'Remove' }).nth(1).click()

    // A confirmed factor stays behind, so the backend deletes free — the
    // inverted predicate's old code demand here was the 400 the removal
    // answered with.
    await vi.waitFor(() => {
      expect(captured.deleted).toEqual({ totpId: 'totp_b', code: '' })
    })
  })

  it('demands the factor code when the removal strips the last confirmed factor', async () => {
    const { screen, captured } = await renderSecurity({
      enrollments: [enrollment('totp_a', 'Aegis on tablet', true)]
    })
    await expect.element(screen.getByText('Aegis on tablet')).toBeVisible()
    await screen.getByRole('button', { name: 'Remove' }).click()

    await expect.element(screen.getByRole('alertdialog')).toBeVisible()
    await screen.getByLabelText('Authenticator or recovery code').fill('039471')
    // The dialog's backdrop intercepts synthetic pointer events; the DOM
    // click is what a real activation reduces to here.
    const continueButton = screen
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Continue' })
      .element()
    if (!(continueButton instanceof HTMLElement)) {
      throw new Error('the continue button is not an element')
    }
    continueButton.click()

    await vi.waitFor(() => {
      expect(captured.deleted).toEqual({ totpId: 'totp_a', code: '039471' })
    })
  })

  it('regenerates the codes behind proof and code, and shows the set once', async () => {
    const { screen, captured } = await renderSecurity({
      enrollments: [enrollment('totp_a', 'Aegis on tablet', true)]
    })
    await screen.getByRole('button', { name: 'Regenerate codes' }).click()
    await prove(screen)

    await expect.element(screen.getByRole('alertdialog')).toBeVisible()
    await screen.getByLabelText('Authenticator or recovery code').fill('039471')
    // The dialog's backdrop intercepts synthetic pointer events; the DOM
    // click is what a real activation reduces to here.
    const continueButton = screen
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Continue' })
      .element()
    if (!(continueButton instanceof HTMLElement)) {
      throw new Error('the continue button is not an element')
    }
    continueButton.click()

    await expect.element(screen.getByText('Your recovery codes — shown once')).toBeVisible()
    await expect.element(screen.getByText('3f9a1c02')).toBeVisible()
    await vi.waitFor(() => {
      expect(captured.proof).toBe('proof_9')
      expect(captured.regenerated).toEqual({ code: '039471' })
    })
  })

  it('disables two-factor behind proof and code', async () => {
    const { screen, captured } = await renderSecurity({
      enrollments: [enrollment('totp_a', 'Aegis on tablet', true)]
    })
    await screen.getByRole('button', { name: 'Disable two-factor' }).click()
    await prove(screen)
    await screen.getByLabelText('Authenticator or recovery code').fill('039471')
    // The dialog's backdrop intercepts synthetic pointer events; the DOM
    // click is what a real activation reduces to here.
    const continueButton = screen
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Continue' })
      .element()
    if (!(continueButton instanceof HTMLElement)) {
      throw new Error('the continue button is not an element')
    }
    continueButton.click()
    await vi.waitFor(() => {
      expect(captured.proof).toBe('proof_9')
      expect(captured.disabled).toEqual({ code: '039471' })
    })
  })

  it('renders the passkey roll with its rename and delete actions', async () => {
    const { screen, captured } = await renderSecurity({
      passkeys: [enrollment('psk_v1', 'Aegis on tablet', true)]
    })
    await expect.element(screen.getByText('Aegis on tablet')).toBeVisible()

    // The rename rides no proof — the credential does not change.
    await screen.getByRole('button', { name: 'Rename' }).click()
    const nameField = screen.getByLabelText('New name')
    await nameField.fill('Roaming key')
    // The dialog's backdrop intercepts synthetic pointer events; the DOM
    // click is what a real activation reduces to here.
    const renameConfirm = screen
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Rename' })
      .element()
    if (!(renameConfirm instanceof HTMLElement)) {
      throw new Error('the rename button is not an element')
    }
    renameConfirm.click()
    await vi.waitFor(() => {
      expect(captured.renamed).toEqual({ credentialId: 'psk_v1', name: 'Roaming key' })
    })
    expect(captured.proof).toBeNull()

    // The delete is step-up guarded — the proof is spent on the call.
    await screen.getByRole('button', { name: 'Delete' }).click()
    await prove(screen)
    await vi.waitFor(() => {
      expect(captured.credentialDeleted).toEqual({
        credentialId: 'psk_v1',
        proof: 'proof_9'
      })
    })
  })

  it('asks the browser for a credential when the enrollment begins', async () => {
    const { screen, captured } = await renderSecurity()
    await screen.getByRole('button', { name: 'Add a passkey' }).click()
    await screen.getByLabelText('Name this passkey').fill('Roaming key')
    const enrollContinue = screen
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Continue' })
      .element()
    if (!(enrollContinue instanceof HTMLElement)) {
      throw new Error('the continue button is not an element')
    }
    enrollContinue.click()
    // The ceremony opened; the browser's create() refuses in a test realm
    // with no authenticator, and the refusal reads in the page's error line.
    await vi.waitFor(() => {
      expect(captured.registrationStarted).toBe(1)
    })
  })
})
