import { create } from '@bufbuild/protobuf'
import { createConnectQueryKey, useMutation } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { ShowOnceSecretDialog } from '#/components/dialogs/-show-once-secret-dialog'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { pageStyles } from '#/styles/pages/page.stylex'
import { CreateOidcClientRequestSchema, OidcClientService } from '~/codegen/federation_pb'
import {
  OidcClientFields,
  emptyClientForm,
  parseLines,
  validateClientForm,
  type OidcClientFormState
} from './-oidc-client-form'

/**
 * The create screen: the form defines the client and the minting happens in
 * the same act — the response's raw secret is the show-once dialog's whole
 * display surface, and acknowledging it carries the operator to the detail.
 */
export function OidcClientNewView() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [state, setState] = useState<OidcClientFormState>(emptyClientForm)
  const [failed, setFailed] = useState<string | null>(null)
  const [minted, setMinted] = useState<{ id: string; secret: string } | null>(null)

  const creating = useMutation(OidcClientService.method.createClient, {
    onSuccess: (response) => {
      setFailed(null)
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: OidcClientService.method.listClients,
          cardinality: 'finite'
        })
      })
      setMinted({ id: response.client?.id ?? '', secret: response.secret })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const submit = () => {
    const invalid = validateClientForm(state)
    if (invalid) {
      setFailed(invalid)
      return
    }
    creating.mutate(
      create(CreateOidcClientRequestSchema, {
        id: state.id.trim() ? state.id.trim() : undefined,
        name: state.name.trim(),
        description: state.description.trim() ? state.description.trim() : undefined,
        callbackUrls: parseLines(state.callbackUrls),
        logoutCallbackUrls: parseLines(state.logoutCallbackUrls),
        launchUrl: state.launchUrl.trim() ? state.launchUrl.trim() : undefined,
        isPublic: state.isPublic,
        pkceEnabled: state.pkceEnabled,
        requiresReauthentication: state.requiresReauthentication,
        requiresPushedAuthorizationRequests: state.requiresPar,
        skipConsent: state.skipConsent,
        accessTokenDurationMinutes: state.accessTokenDuration
          ? BigInt(state.accessTokenDuration)
          : undefined,
        refreshTokenDurationMinutes: state.refreshTokenDuration
          ? BigInt(state.refreshTokenDuration)
          : undefined,
        backchannelLogoutUri: state.backchannelLogoutUri.trim()
          ? state.backchannelLogoutUri.trim()
          : undefined,
        backchannelLogoutSessionRequired: state.backchannelSessionRequired,
        allowedGrantTypes: state.grantTypes
      })
    )
  }

  return (
    <div
      {...stylex.props(
        pageStyles.container,
        pageStyles.containerPadMedium,
        pageStyles.containerPadLarge,
        pageStyles.containerPadXLarge
      )}
    >
      <div {...stylex.props(pageStyles.header)}>
        <div {...stylex.props(pageStyles.headerLeft)}>
          <Text
            render={<p />}
            variant='caption-1'
            weight='semibold'
            color='primary'
            style={pageStyles.kicker}
          >
            Administration
          </Text>
          <Text render={<h1 />} variant='featured-4' weight='bold'>
            New OIDC client
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The first secret answers this form — shown once, stored by you.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Client definition</CardTitle>
            <CardDescription>
              Everything the protocol needs — the redirect list above all.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <OidcClientFields mode='create' state={state} onChange={setState} />
            {failed ? (
              <Text render={<p />} variant='body-2' color='critical'>
                {failed}
              </Text>
            ) : null}
            <div className='mt-4 flex justify-end'>
              <Button disabled={creating.isPending} onClick={submit}>
                {creating.isPending ? <Spinner /> : null}
                Create client
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>

      <ShowOnceSecretDialog
        open={minted !== null}
        title='The client secret'
        description={
          minted
            ? `The relying party authenticates with this secret, beside the client id ${minted.id}.`
            : ''
        }
        secret={minted?.secret ?? ''}
        onOpenChange={(next) => {
          if (!next && minted) {
            setMinted(null)
            void navigate({ to: '/admin/oidc-clients/$id', params: { id: minted.id } })
          }
        }}
      />
    </div>
  )
}
