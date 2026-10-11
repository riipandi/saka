import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { useQuery } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue
} from 'uilibs/components/base/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from 'uilibs/components/base/tabs'
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
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemTitle
} from 'uilibs/components/extra/item'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { LIST_MAX_LIMIT, listPageInput, decodeListMetadata } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { ScopeChip } from '#/routes/(app)/account/-authorized-apps-view'
import { pageStyles } from '#/styles/pages/page.stylex'
import { OidcConsentService } from '~/codegen/federation_pb'
import { ListUsersRequestSchema, UserService } from '~/codegen/identity_pb'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

/** The wire's Timestamp printed as the operator reads it. */
function formatInstant(value?: Timestamp | null): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The consents screen (phase 5): the administrative half of the consent
 * ledger. The wide view answers every row deployment-wide, paged; the
 * account view reads one account's ledger whole.
 */
export function ConsentsView() {
  const [userSubject, setUserSubject] = useState('')
  const [allPage, setAllPage] = useState(1)

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
            Consents
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The consent ledger — which account let which relying party in, and what the consent
            covers. The ledger is read here; withdrawal stays with the account.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Tabs defaultValue='all'>
          <TabsList>
            <TabsTrigger value='all'>Deployment-wide</TabsTrigger>
            <TabsTrigger value='account'>By account</TabsTrigger>
          </TabsList>
          <TabsContent value='all'>
            <AllConsentsCard page={allPage} onPage={setAllPage} />
          </TabsContent>
          <TabsContent value='account'>
            <SubjectPickCard onSubject={(id) => setUserSubject(id ?? '')} />
            <UserConsentsCard userId={userSubject} />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}

function SubjectPickCard({ onSubject }: { onSubject: (id: string | null) => void }) {
  const users = useQuery(
    UserService.method.listUsers,
    create(ListUsersRequestSchema, listPageInput(1, LIST_MAX_LIMIT)),
    { retry: false }
  )
  const rows = users.data?.users ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle>One account's ledger</CardTitle>
        <CardDescription>Pick the account whose consents the ledger should answer.</CardDescription>
      </CardHeader>
      <CardContent>
        <Select onValueChange={(next) => onSubject(typeof next === 'string' ? next : null)}>
          <SelectTrigger aria-label='Account'>
            <SelectValue placeholder='pick an account' />
          </SelectTrigger>
          <SelectContent>
            {rows.map((user) => (
              <SelectItem key={user.id} value={user.id}>
                {user.displayName !== '' ? `${user.displayName} (${user.email})` : user.email}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </CardContent>
    </Card>
  )
}

function AllConsentsCard({ page, onPage }: { page: number; onPage: (next: number) => void }) {
  const consents = useQuery(
    OidcConsentService.method.listAllAuthorizedClients,
    listPageInput(page),
    { retry: false }
  )
  const rows = consents.data?.entries ?? []
  const summary = decodeListMetadata(consents.data?.metadata)

  return (
    <Card>
      <CardHeader>
        <CardTitle>Every consent</CardTitle>
        <CardDescription>
          The ledger rows the deployment holds, most recently used first.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {consents.isPending ? (
          <Spinner />
        ) : consents.isError ? (
          <Text variant='body-2' color='critical'>
            {getErrorMessage(consents.error)}
          </Text>
        ) : rows.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No consents recorded</EmptyTitle>
              <EmptyDescription>No account has authorized a relying party yet.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <>
            <ItemGroup>
              {rows.map((entry) => (
                <Item key={`${entry.userId}:${entry.client?.id ?? ''}`}>
                  <ItemContent>
                    <ItemTitle>{entry.client?.name ?? '(unknown client)'}</ItemTitle>
                    <ItemDescription>
                      account {entry.userId} · client id {entry.client?.id ?? '—'} · last used{' '}
                      {formatInstant(entry.lastUsedAt)}
                    </ItemDescription>
                    <div {...stylex.props(styles.scopes)}>
                      {entry.scopes.map((scope) => (
                        <ScopeChip key={scope} scope={scope} />
                      ))}
                    </div>
                  </ItemContent>
                </Item>
              ))}
            </ItemGroup>
            {summary !== null && summary.totalPages !== null && summary.totalPages > 1 && (
              <div {...stylex.props(styles.pager)}>
                <Button
                  variant='outline'
                  size='sm'
                  disabled={summary.page <= 1}
                  onClick={() => onPage(summary.page - 1)}
                >
                  Previous
                </Button>
                <Text variant='body-2' color='neutral-faded'>
                  Page {summary.page} of {summary.totalPages}
                </Text>
                <Button
                  variant='outline'
                  size='sm'
                  disabled={summary.page >= (summary.totalPages ?? 1)}
                  onClick={() => onPage(summary.page + 1)}
                >
                  Next
                </Button>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}

function UserConsentsCard({ userId }: { userId: string }) {
  const consents = useQuery(
    OidcConsentService.method.listUserAuthorizedClients,
    { userId },
    { enabled: userId !== '', retry: false }
  )
  const rows = consents.data?.clients ?? []

  if (userId === '') return null

  return (
    <Card>
      <CardHeader>
        <CardTitle>The account's consents</CardTitle>
        <CardDescription>What this account has let in, most recently used first.</CardDescription>
      </CardHeader>
      <CardContent>
        {consents.isPending ? (
          <Spinner />
        ) : consents.isError ? (
          <Text variant='body-2' color='critical'>
            {getErrorMessage(consents.error)}
          </Text>
        ) : rows.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>Nothing authorized</EmptyTitle>
              <EmptyDescription>This account has no consent on the ledger.</EmptyDescription>
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
              </Item>
            ))}
          </ItemGroup>
        )}
      </CardContent>
    </Card>
  )
}

const styles = stylex.create({
  scopes: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 6,
    marginTop: 4
  },
  pager: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'flex-end',
    gap: 12,
    marginTop: 16
  }
})
