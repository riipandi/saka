import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle
} from 'uilibs/components/base/alert-dialog'
import { Button } from 'uilibs/components/base/button'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from 'uilibs/components/extra/empty'
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemTitle
} from 'uilibs/components/extra/item'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { pageStyles } from '#/styles/pages/page.stylex'
import { OidcConsentService, RevokeMyAuthorizedClientRequestSchema } from '~/codegen/federation_pb'
import type { AuthorizedOidcClient } from '~/codegen/federation_pb'
const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

/** The wire's Timestamp printed as the operator reads it. */
function formatInstant(value?: Timestamp | null): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The authorized apps screen (phase 5): the relying parties this account
 * has consented to, with the scopes each consent carries. Withdrawing one
 * kills the client's live grants and tokens for the account with the
 * ledger row.
 */
export function AuthorizedAppsView() {
  const queryClient = useQueryClient()
  const consents = useQuery(OidcConsentService.method.listMyAuthorizedClients, {}, { retry: false })
  const [revoking, setRevoking] = useState<AuthorizedOidcClient | null>(null)
  const [removeFailed, setRemoveFailed] = useState<string | null>(null)

  const listKey = createConnectQueryKey({
    schema: OidcConsentService.method.listMyAuthorizedClients,
    cardinality: 'finite'
  })

  const revoke = useMutation(OidcConsentService.method.revokeMyAuthorizedClient, {
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: listKey })
      setRevoking(null)
      setRemoveFailed(null)
    },
    onError: (error) => setRemoveFailed(getErrorMessage(error))
  })

  const rows = consents.data?.clients ?? []

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
            Account
          </Text>
          <Text render={<h1 />} variant='featured-4' weight='bold'>
            Authorized apps
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The applications you signed in to with this account, and what each one may read.
            Withdrawing a consent kills its live grants.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Your consents</CardTitle>
            <CardDescription>
              Most recently used first; a consent you no longer recognize is one to withdraw.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {consents.isPending ? (
              <Spinner />
            ) : consents.isError ? (
              <Text variant='body-2' color='critical'>
                {consents.error.message}
              </Text>
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No authorized apps</EmptyTitle>
                  <EmptyDescription>
                    When you sign in to an app with this account, its consent lands here.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <ItemGroup>
                {rows.map((entry) => (
                  <Item key={entry.client?.id ?? ''}>
                    <ItemContent>
                      <ItemTitle>{entry.client?.name ?? '(unknown client)'}</ItemTitle>
                      <ItemDescription>
                        client id {entry.client?.id ?? '—'} · last used{' '}
                        {formatInstant(entry.lastUsedAt)}
                      </ItemDescription>
                      <div {...stylex.props(styles.scopes)}>
                        {entry.scopes.map((scope) => (
                          <ScopeChip key={scope} scope={scope} />
                        ))}
                      </div>
                    </ItemContent>
                    <ItemActions>
                      <Button
                        variant='ghost'
                        size='sm'
                        onClick={() => {
                          setRemoveFailed(null)
                          setRevoking(entry)
                        }}
                      >
                        Withdraw
                      </Button>
                    </ItemActions>
                  </Item>
                ))}
              </ItemGroup>
            )}
          </CardContent>
        </Card>
      </div>

      <AlertDialog
        open={revoking !== null}
        onOpenChange={(next) => {
          if (!next) setRevoking(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Withdraw the consent for {revoking?.client?.name ?? ''}?
            </AlertDialogTitle>
            <AlertDialogDescription>
              The app loses every live grant and token it holds for this account. It asks for your
              consent again the next time you sign in to it.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {removeFailed !== null && (
            <Text variant='body-2' color='critical'>
              {removeFailed}
            </Text>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (revoking?.client) {
                  revoke.mutate(
                    create(RevokeMyAuthorizedClientRequestSchema, { clientId: revoking.client.id })
                  )
                }
              }}
            >
              {revoke.isPending ? <Spinner /> : 'Withdraw'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

/** The scope words a consent carries, worded for the reader: the offline
 * scope is a persistent access the app keeps renewing, and the login hint
 * scope only ever prefills a form. */
export function ScopeChip({ scope }: { scope: string }) {
  if (scope === 'offline_access') {
    return <Badge variant='primary'>offline_access — persistent access</Badge>
  }
  if (scope === 'login_hint') {
    return <Badge variant='secondary'>login_hint — prefills the sign-in form</Badge>
  }
  return <Badge variant='outline'>{scope}</Badge>
}

const styles = stylex.create({
  scopes: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 6,
    marginTop: 4
  }
})
