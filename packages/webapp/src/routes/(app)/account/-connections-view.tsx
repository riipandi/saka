import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createClient } from '@connectrpc/connect'
import { createConnectQueryKey, useQuery, useTransport } from '@connectrpc/connect-query'
import { Unlink } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from 'uilibs/components/base/dialog'
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
import { ReauthenticateDialog } from '#/libraries/reauth/-reauthenticate-dialog'
import { StepUpCancelled } from '#/libraries/reauth/reauth'
import { useStepUp } from '#/libraries/reauth/use-step-up'
import { pageStyles } from '#/styles/pages/page.stylex'
import { OAuthSSOService, UnlinkConnectionRequestSchema } from '~/codegen/authn_pb'
import type { OAuthLinkedAccount } from '~/codegen/authn_pb'
import { AccountFrame } from './-account-frame'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

/** The wire's Timestamp printed as the operator reads it. */
function formatInstant(value?: Timestamp | null): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The connected accounts screen (phase 4): the provider identities the
 * calling account holds. Unlinking spends a step-up proof — the last
 * credential of an account that keeps no password and no passkey is
 * refused — and the provider tokens a binding carries are opened for
 * this one read, never stored.
 */
export function ConnectionsView({ embedded = false }: { embedded?: boolean } = {}) {
  const queryClient = useQueryClient()
  const transport = useTransport()
  const linkedRpc = useMemo(() => createClient(OAuthSSOService, transport), [transport])
  const stepUp = useStepUp()

  const linked = useQuery(OAuthSSOService.method.listLinkedConnections, {}, { retry: false })
  const listKey = createConnectQueryKey({
    schema: OAuthSSOService.method.listLinkedConnections,
    cardinality: 'finite'
  })

  const [actionError, setActionError] = useState<string | null>(null)
  const [unlinkedId, setUnlinkedId] = useState<string | null>(null)
  const [tokensFor, setTokensFor] = useState<OAuthLinkedAccount | null>(null)

  const unlink = (account: OAuthLinkedAccount) => {
    setUnlinkedId(account.id)
    stepUp
      .run((options) =>
        linkedRpc.unlinkConnection(
          create(UnlinkConnectionRequestSchema, { linkedAccountId: account.id }),
          options
        )
      )
      .then(() => {
        setActionError(null)
        return queryClient.invalidateQueries({ queryKey: listKey })
      })
      .catch((unlinkError) => {
        if (unlinkError instanceof StepUpCancelled) return
        setActionError(getErrorMessage(unlinkError))
      })
      .finally(() => setUnlinkedId(null))
  }

  const rows = linked.data?.linkedAccounts ?? []

  return (
    <AccountFrame
      embedded={embedded}
      title='Connected accounts'
      description='The provider identities bound to this account — sign in through them, and unlink the ones you no longer hold.'
    >
      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Linked providers</CardTitle>
            <CardDescription>
              Unlinking spends a fresh sign-in proof; an account left with no other credential is
              refused until it sets a password or a passkey.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {linked.isPending ? (
              <Spinner />
            ) : linked.isError ? (
              <Text variant='body-2' color='critical'>
                {linked.error.message}
              </Text>
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No linked accounts</EmptyTitle>
                  <EmptyDescription>
                    Sign in through a provider and its identity lands here.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <ItemGroup>
                {rows.map((account) => (
                  <Item key={account.id}>
                    <ItemContent>
                      <ItemTitle>{account.provider}</ItemTitle>
                      <ItemDescription>
                        {account.email} · linked {formatInstant(account.createdAt)}
                      </ItemDescription>
                    </ItemContent>
                    <ItemActions>
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => {
                          setTokensFor(account)
                        }}
                      >
                        Tokens
                      </Button>
                      <Button
                        variant='ghost'
                        size='sm'
                        disabled={unlinkedId !== null}
                        onClick={() => unlink(account)}
                      >
                        <Unlink />
                        {unlinkedId === account.id ? 'Unlinking…' : 'Unlink'}
                      </Button>
                    </ItemActions>
                  </Item>
                ))}
              </ItemGroup>
            )}
            {actionError !== null && (
              <Text render={<p />} variant='body-2' color='critical'>
                {actionError}
              </Text>
            )}
          </CardContent>
        </Card>
      </div>

      <TokensDialog account={tokensFor} onOpenChange={(next) => !next && setTokensFor(null)} />

      <ReauthenticateDialog
        open={stepUp.dialogOpen}
        onProven={stepUp.onProven}
        onDismissed={stepUp.onDismissed}
      />
    </AccountFrame>
  )
}

/** The provider tokens one binding carries, opened from their seal for the
 * one read — a binding the provider answered no tokens for shows the empty
 * as the fact. */
function TokensDialog({
  account,
  onOpenChange
}: {
  account: OAuthLinkedAccount | null
  onOpenChange: (open: boolean) => void
}) {
  const tokens = useQuery(
    OAuthSSOService.method.getLinkedAccountTokens,
    { linkedAccountId: account?.id ?? '' },
    { enabled: account !== null, retry: false }
  )

  return (
    <Dialog open={account !== null} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{account?.provider ?? ''} tokens</DialogTitle>
          <DialogDescription>
            The credentials the provider issued at sign-in, opened for this one read — treat them
            like a password.
          </DialogDescription>
        </DialogHeader>
        {tokens.isPending ? (
          <Spinner />
        ) : tokens.isError ? (
          <Text variant='body-2' color='critical'>
            {tokens.error.message}
          </Text>
        ) : (
          <div {...stylex.props(styles.tokens)}>
            <Text render={<p />} variant='caption-1' weight='semibold'>
              Access token
            </Text>
            <Text render={<p />} variant='body-2' style={styles.mono}>
              {tokens.data?.accessToken === ''
                ? '(none — the provider answered no tokens)'
                : tokens.data?.accessToken}
            </Text>
            <Text render={<p />} variant='caption-1' weight='semibold'>
              Refresh token
            </Text>
            <Text render={<p />} variant='body-2' style={styles.mono}>
              {tokens.data?.refreshToken === ''
                ? '(none — the provider answered no tokens)'
                : tokens.data?.refreshToken}
            </Text>
            <Text render={<p />} variant='caption-1' weight='semibold'>
              Access token expires
            </Text>
            <Text render={<p />} variant='body-2' style={styles.mono}>
              {tokens.data?.expiresAt === undefined
                ? '(the provider named no expiry)'
                : formatInstant(tokens.data.expiresAt)}
            </Text>
          </div>
        )}
        <DialogFooter>
          <Button variant='ghost' onClick={() => onOpenChange(false)}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

const styles = stylex.create({
  tokens: {
    display: 'grid',
    gap: 6
  },
  mono: {
    fontFamily: 'monospace',
    wordBreak: 'break-all'
  }
})
