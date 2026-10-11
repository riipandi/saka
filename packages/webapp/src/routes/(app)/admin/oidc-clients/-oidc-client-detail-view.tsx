import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { Delete, ImagePlus, RefreshCw, RotateCcw } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger
} from 'uilibs/components/base/alert-dialog'
import { Avatar, AvatarFallback, AvatarImage } from 'uilibs/components/base/avatar'
import { Button } from 'uilibs/components/base/button'
import { Checkbox } from 'uilibs/components/base/checkbox'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardAction,
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
import { LIST_MAX_LIMIT, listPageInput } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { Can } from '#/libraries/guard/can'
import { pageStyles } from '#/styles/pages/page.stylex'
import {
  DeleteOidcClientRequestSchema,
  DeleteOidcClientSecretRequestSchema,
  DeleteOidcClientLogoRequestSchema,
  GetOidcClientRequestSchema,
  ListOidcClientSecretsRequestSchema,
  OidcClientService,
  RefreshOidcClientRequestSchema,
  UpdateOidcClientRequestSchema,
  UpdateOidcClientAllowedUserGroupsRequestSchema,
  UploadOidcClientLogoRequestSchema,
  type OidcClient
} from '~/codegen/federation_pb'
import { ListUserGroupsRequestSchema, UserGroupService } from '~/codegen/identity_pb'
import { PreviewClientDialog } from './-client-preview-drawer'
import { MintSecretDialog } from './-client-secrets-dialog'
import {
  OidcClientFields,
  clientFormFromClient,
  parseLines,
  validateClientForm,
  type OidcClientFormState
} from './-oidc-client-form'
import { ScimProviderCard } from './-scim-provider-card'

/** The largest logo the backend accepts — the client pre-check is UX, the
 * byte cap is the server's. */
const MAX_LOGO_BYTES = 2 * 1024 * 1024
const LOGO_KINDS = ['image/png', 'image/jpeg', 'image/webp']

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

/** The wire's Timestamp, a Date, or an ISO string — every instant the
 * detail's cards print goes through one formatter. */
function formatInstant(value?: Timestamp | Date | string | null): string {
  if (!value) return '—'
  if (value instanceof Date) return instantFormat.format(value)
  if (typeof value === 'string') {
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? '—' : instantFormat.format(parsed)
  }
  return formatWireInstant(value)
}

function formatWireInstant(value: Timestamp): string {
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The client detail (phase 1): one client's whole administration — the
 * fields, the secrets whose values exist only at their minting, the logo
 * over the binary field, the group restriction, the claim preview, and the
 * CIMD metadata refresh a document-backed client alone can use.
 */
export function OidcClientDetailView({ clientId }: { clientId: string }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [previewOpen, setPreviewOpen] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const client = useQuery(
    OidcClientService.method.getClient,
    create(GetOidcClientRequestSchema, { id: clientId }),
    { retry: false }
  )

  const invalidateClient = () =>
    queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: OidcClientService.method.getClient,
        cardinality: 'finite'
      })
    })

  const deleting = useMutation(OidcClientService.method.deleteClient, {
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: OidcClientService.method.listClients,
          cardinality: 'finite'
        })
      })
      void navigate({
        to: '/admin/oidc-clients',
        search: { page: undefined, search: undefined }
      })
    },
    onError: (error) => setActionError(getErrorMessage(error))
  })

  const refreshing = useMutation(OidcClientService.method.refreshClient, {
    onSuccess: () => {
      setActionError(null)
      void invalidateClient()
    },
    onError: (error) => setActionError(getErrorMessage(error))
  })

  const view = client.data?.client

  if (client.isPending) {
    return (
      <div {...stylex.props(pageStyles.container, pageStyles.containerPadXLarge)}>
        <Spinner />
      </div>
    )
  }

  if (client.isError || !view) {
    return (
      <div {...stylex.props(pageStyles.container, pageStyles.containerPadXLarge)}>
        <Empty>
          <EmptyHeader>
            <EmptyTitle>Client not found</EmptyTitle>
            <EmptyDescription>{getErrorMessage(client.error)}</EmptyDescription>
          </EmptyHeader>
        </Empty>
        <Button
          variant='outline'
          render={<Link to='/admin/oidc-clients' search={{ page: undefined, search: undefined }} />}
        >
          Back to the list
        </Button>
      </div>
    )
  }

  const isCimd = view.clientType === 'cimd'

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
            <Link to='/admin/oidc-clients' search={{ page: undefined, search: undefined }}>
              OIDC clients
            </Link>{' '}
            / {view.id}
          </Text>
          <Text render={<h1 />} variant='featured-4' weight='bold'>
            {view.name}
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            Created {formatInstant(view.createdAt)} ·{' '}
            {isCimd ? 'materialized from a metadata document' : 'registered here'}
          </Text>
        </div>
        <div {...stylex.props(localStyles.headerActions)}>
          <Can permission='oidc_client:*:update'>
            <Button variant='outline' size='sm' onClick={() => setPreviewOpen(true)}>
              Preview claims
            </Button>
            {isCimd ? (
              <Button
                variant='outline'
                size='sm'
                disabled={refreshing.isPending}
                onClick={() =>
                  refreshing.mutate(create(RefreshOidcClientRequestSchema, { id: view.id }))
                }
              >
                {refreshing.isPending ? <Spinner /> : <RefreshCw size={16} />}
                Refresh metadata
              </Button>
            ) : null}
            <Can permission='oidc_client:*:delete'>
              <AlertDialog>
                <AlertDialogTrigger render={<Button variant='destructive' size='sm' />}>
                  <Delete size={16} />
                  Delete
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>Delete {view.name}?</AlertDialogTitle>
                    <AlertDialogDescription>
                      The codes, sessions, and grants that name this client die with it. This cannot
                      be undone.
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>Cancel</AlertDialogCancel>
                    <AlertDialogAction
                      onClick={() =>
                        deleting.mutate(create(DeleteOidcClientRequestSchema, { id: view.id }))
                      }
                    >
                      Delete client
                    </AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            </Can>
          </Can>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        {actionError ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {actionError}
          </Text>
        ) : null}

        <SecretsCard clientId={view.id} />
        <ScimProviderCard clientId={view.id} />
        <LogoCard client={view} onChanged={() => void invalidateClient()} />
        <GroupsCard client={view} onChanged={() => void invalidateClient()} />
        <EditCard client={view} onSaved={() => void invalidateClient()} />

        {isCimd && view.metadataExpiresAt ? (
          <Text variant='body-2' color='neutral-faded'>
            The surface may re-fetch the metadata document on its own after{' '}
            {formatInstant(view.metadataExpiresAt)}.
          </Text>
        ) : null}
      </div>

      <PreviewClientDialog clientId={view.id} open={previewOpen} onOpenChange={setPreviewOpen} />
    </div>
  )
}

function SecretsCard({ clientId }: { clientId: string }) {
  const secrets = useQuery(
    OidcClientService.method.listSecrets,
    create(ListOidcClientSecretsRequestSchema, { id: clientId })
  )
  const rows = secrets.data?.credentials?.secrets ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle>Secrets</CardTitle>
        <CardDescription>
          Only the hash is stored — the prefix is what an operator reads a leak from.
        </CardDescription>
        <CardAction>
          <Can permission='oidc_client:*:update'>
            <MintSecretDialog clientId={clientId} />
          </Can>
        </CardAction>
      </CardHeader>
      <CardContent>
        {secrets.isPending ? (
          <Spinner />
        ) : rows.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No live secrets</EmptyTitle>
              <EmptyDescription>
                A confidential client with no secret authenticates nothing new; its public-client
                grants are untouched.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ItemGroup>
            {rows.map((secret) => (
              <SecretRow
                key={secret.id}
                clientId={clientId}
                secretId={secret.id}
                prefix={secret.prefix}
                active={secret.isActive}
                createdAt={secret.createdAt}
                expiresAt={secret.expiresAt}
              />
            ))}
          </ItemGroup>
        )}
      </CardContent>
    </Card>
  )
}

function SecretRow({
  clientId,
  secretId,
  prefix,
  active,
  createdAt,
  expiresAt
}: {
  clientId: string
  secretId: string
  prefix: string
  active: boolean
  createdAt?: Timestamp
  expiresAt?: Timestamp
}) {
  const queryClient = useQueryClient()
  const [failed, setFailed] = useState<string | null>(null)
  const withdrawing = useMutation(OidcClientService.method.deleteSecret, {
    onSuccess: () => {
      setFailed(null)
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: OidcClientService.method.listSecrets,
          cardinality: 'finite'
        })
      })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <Item variant='outline'>
      <ItemContent>
        <ItemTitle>
          <span className='font-mono'>{prefix}••••••••</span>
        </ItemTitle>
        <ItemDescription>
          minted {formatInstant(createdAt)}
          {expiresAt ? ` · expires ${formatInstant(expiresAt)}` : ' · never expires'}
        </ItemDescription>
      </ItemContent>
      <ItemActions>
        <Badge variant={active ? 'primary' : 'secondary'}>{active ? 'Active' : 'Expired'}</Badge>
        <Can permission='oidc_client:*:update'>
          <AlertDialog>
            <AlertDialogTrigger render={<Button variant='outline' size='sm' />}>
              <Delete size={16} />
              Withdraw
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>Withdraw the secret {prefix}••••?</AlertDialogTitle>
                <AlertDialogDescription>
                  The relying party can no longer authenticate with it. This cannot be undone.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction
                  onClick={() =>
                    withdrawing.mutate(
                      create(DeleteOidcClientSecretRequestSchema, { id: clientId, secretId })
                    )
                  }
                >
                  Withdraw
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </Can>
      </ItemActions>
      {failed ? (
        <Text render={<p />} variant='body-2' color='critical'>
          {failed}
        </Text>
      ) : null}
    </Item>
  )
}

function LogoCard({ client, onChanged }: { client: OidcClient; onChanged: () => void }) {
  const [failed, setFailed] = useState<string | null>(null)
  const [busy, setBusy] = useState<'upload' | 'reset' | null>(null)

  const uploading = useMutation(OidcClientService.method.uploadLogo, {
    onSuccess: () => {
      setFailed(null)
      onChanged()
    },
    onError: (error) => setFailed(getErrorMessage(error)),
    onSettled: () => setBusy(null)
  })
  const removing = useMutation(OidcClientService.method.deleteLogo, {
    onSuccess: () => {
      setFailed(null)
      onChanged()
    },
    onError: (error) => setFailed(getErrorMessage(error)),
    onSettled: () => setBusy(null)
  })

  const upload = async (file: File) => {
    if (!LOGO_KINDS.includes(file.type)) {
      setFailed('The logo must be a PNG, JPEG, or WebP image — an SVG is refused.')
      return
    }
    if (file.size > MAX_LOGO_BYTES) {
      setFailed('The logo must be at most 2 MiB.')
      return
    }
    setBusy('upload')
    // The bytes decide the kind, never a declared content type — the field
    // carries the raw image and the server reads its magic bytes.
    const logo = new Uint8Array(await file.arrayBuffer())
    uploading.mutate(create(UploadOidcClientLogoRequestSchema, { id: client.id, logo }))
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Logo</CardTitle>
        <CardDescription>
          A square image under 2 MiB — PNG, JPEG, or WebP. An SVG is refused.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div {...stylex.props(localStyles.logoRow)}>
          <Avatar>
            {client.hasLogo && client.logoUrl ? (
              <AvatarImage src={client.logoUrl} alt={client.name} />
            ) : null}
            <AvatarFallback>{client.id.charAt(0).toUpperCase()}</AvatarFallback>
          </Avatar>
          <div {...stylex.props(localStyles.logoActions)}>
            <Can permission='oidc_client:*:update'>
              <label>
                <input
                  type='file'
                  accept={LOGO_KINDS.join(',')}
                  hidden
                  onChange={(event) => {
                    const file = event.target.files?.[0]
                    if (file) void upload(file)
                  }}
                />
                <Button variant='outline' size='sm' disabled={busy !== null}>
                  {busy === 'upload' ? <Spinner /> : <ImagePlus size={16} />}
                  Upload
                </Button>
              </label>
              {client.hasLogo ? (
                <Button
                  variant='outline'
                  size='sm'
                  disabled={busy !== null}
                  onClick={() => {
                    setBusy('reset')
                    removing.mutate(create(DeleteOidcClientLogoRequestSchema, { id: client.id }))
                  }}
                >
                  {busy === 'reset' ? <Spinner /> : <RotateCcw size={16} />}
                  Remove
                </Button>
              ) : null}
            </Can>
          </div>
        </div>
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        ) : null}
      </CardContent>
    </Card>
  )
}

function GroupsCard({ client, onChanged }: { client: OidcClient; onChanged: () => void }) {
  const [picked, setPicked] = useState<Set<string>>(
    new Set(client.allowedUserGroups.map((group) => group.id))
  )
  const [failed, setFailed] = useState<string | null>(null)

  const groups = useQuery(
    UserGroupService.method.listUserGroups,
    create(ListUserGroupsRequestSchema, listPageInput(1, LIST_MAX_LIMIT))
  )
  const rows = groups.data?.groups ?? []

  const saving = useMutation(OidcClientService.method.updateAllowedUserGroups, {
    onSuccess: () => {
      setFailed(null)
      onChanged()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const toggle = (groupId: string, on: boolean) =>
    setPicked((prev) => {
      const next = new Set(prev)
      if (on) next.add(groupId)
      else next.delete(groupId)
      return next
    })

  return (
    <Card>
      <CardHeader>
        <CardTitle>Allowed groups</CardTitle>
        <CardDescription>
          The restriction stands while this list names groups — or while the restriction flag from
          creation stands. An account outside them is refused at the authorization endpoint; an
          empty list under a standing restriction admits nobody.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div {...stylex.props(localStyles.groupList)}>
          {rows.map((group) => (
            <label key={group.id} className='flex items-center gap-2 text-sm'>
              <Checkbox
                checked={picked.has(group.id)}
                onCheckedChange={(checked) => toggle(group.id, checked)}
              />
              {group.name}
            </label>
          ))}
        </div>
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        ) : null}
        <div {...stylex.props(localStyles.saveRow)}>
          <Can permission='oidc_client:*:update'>
            <Button
              size='sm'
              disabled={saving.isPending}
              onClick={() =>
                saving.mutate(
                  create(UpdateOidcClientAllowedUserGroupsRequestSchema, {
                    id: client.id,
                    userGroupIds: Array.from(picked)
                  })
                )
              }
            >
              {saving.isPending ? <Spinner /> : null}
              Save groups
            </Button>
          </Can>
        </div>
      </CardContent>
    </Card>
  )
}

function EditCard({ client, onSaved }: { client: OidcClient; onSaved: () => void }) {
  const [state, setState] = useState<OidcClientFormState>(clientFormFromClient(client))
  const [failed, setFailed] = useState<string | null>(null)

  // A refetch replaces the seed once — the form is the operator's scratch,
  // not a mirror the server rewrites under them.
  useEffect(() => {
    setState(clientFormFromClient(client))
  }, [client])

  const saving = useMutation(OidcClientService.method.updateClient, {
    onSuccess: () => {
      setFailed(null)
      onSaved()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const submit = () => {
    const invalid = validateClientForm(state)
    if (invalid) {
      setFailed(invalid)
      return
    }
    saving.mutate(
      create(UpdateOidcClientRequestSchema, {
        id: client.id,
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
    <Card>
      <CardHeader>
        <CardTitle>Client definition</CardTitle>
        <CardDescription>
          The fields a full replace carries — secrets and groups aside.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <OidcClientFields mode='edit' state={state} onChange={setState} />
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        ) : null}
        <div {...stylex.props(localStyles.saveRow)}>
          <Can permission='oidc_client:*:update'>
            <Button disabled={saving.isPending} onClick={submit}>
              {saving.isPending ? <Spinner /> : null}
              Save changes
            </Button>
          </Can>
        </div>
      </CardContent>
    </Card>
  )
}

const localStyles = stylex.create({
  headerActions: {
    display: 'flex',
    gap: 8,
    alignItems: 'center'
  },
  logoRow: {
    display: 'flex',
    alignItems: 'center',
    gap: 16
  },
  logoActions: {
    display: 'flex',
    gap: 8
  },
  groupList: {
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
    maxHeight: '14rem',
    overflowY: 'auto'
  },
  saveRow: {
    display: 'flex',
    justifyContent: 'flex-end',
    marginTop: 12
  }
})
