import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import { Code, ConnectError } from '@connectrpc/connect'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { RefreshCw } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
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
import { Button } from 'uilibs/components/base/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from 'uilibs/components/base/dialog'
import { Field, FieldDescription, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { fontFamily } from 'uilibs/styles/tokens.stylex'
import { ShowOnceSecretDialog } from '#/components/dialogs/-show-once-secret-dialog'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { Can } from '#/libraries/guard/can'
import {
  CreateScimProviderRequestSchema,
  DeleteScimProviderRequestSchema,
  GetScimProviderRequestSchema,
  ScimProviderService,
  SyncScimProviderRequestSchema,
  UpdateScimProviderRequestSchema,
  type ScimProvider
} from '~/codegen/federation_pb'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

/**
 * The client's outbound provisioning target (phase 2): one provider per
 * client, its token shown once at the attach, a pass that runs on demand.
 * The remote data a deletion leaves alone — the row's death only ends the
 * sync.
 */
export function ScimProviderCard({ clientId }: { clientId: string }) {
  const queryClient = useQueryClient()
  const [failed, setFailed] = useState<string | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [syncOutcome, setSyncOutcome] = useState<string | null>(null)

  const provider = useQuery(
    ScimProviderService.method.getByClient,
    create(GetScimProviderRequestSchema, { clientId }),
    { retry: false }
  )
  const view = provider.data?.provider
  // A provider the client does not have is the not-found code — the card's
  // "not configured" state, not an error the operator reads as one.
  const unconfigured = !provider.isPending && (provider.isError || !view)
  const refusal =
    unconfigured &&
    provider.isError &&
    !(provider.error instanceof ConnectError && provider.error.code === Code.NotFound)
      ? getErrorMessage(provider.error)
      : null

  const invalidate = () =>
    queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: ScimProviderService.method.getByClient,
        cardinality: 'finite'
      })
    })

  const syncing = useMutation(ScimProviderService.method.sync, {
    onSuccess: (response) => {
      setFailed(null)
      setSyncOutcome(
        `Pass complete: ${response.usersCreated} accounts created, ${response.usersUpdated} updated, ` +
          `${response.usersDeleted} removed; ${response.groupsCreated} groups created, ` +
          `${response.groupsUpdated} updated, ${response.groupsDeleted} removed.`
      )
      void invalidate()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>SCIM provisioning</CardTitle>
        <CardDescription>
          The outbound target the client's visible accounts and groups sync to over SCIM 2.0.
        </CardDescription>
        {view && !unconfigured ? (
          <CardAction>
            <div {...stylex.props(scimStyles.row)}>
              <Can permission='scim_provider:*:update'>
                <Button variant='outline' size='sm' onClick={() => setDialogOpen(true)}>
                  Update
                </Button>
              </Can>
              <Can permission='scim_provider:*:sync'>
                <Button
                  variant='outline'
                  size='sm'
                  disabled={syncing.isPending}
                  onClick={() =>
                    syncing.mutate(create(SyncScimProviderRequestSchema, { id: view.id }))
                  }
                >
                  {syncing.isPending ? <Spinner /> : <RefreshCw size={16} />}
                  Sync now
                </Button>
              </Can>
              <Can permission='scim_provider:*:delete'>
                <DeleteProviderButton providerId={view.id} />
              </Can>
            </div>
          </CardAction>
        ) : null}
      </CardHeader>
      <CardContent>
        {provider.isPending ? (
          <Spinner />
        ) : view && !unconfigured ? (
          <div {...stylex.props(scimStyles.stackTight)}>
            <Text render={<p />} variant='body-2'>
              Endpoint: <span {...stylex.props(scimStyles.mono)}>{view.endpoint}</span>
            </Text>
            <Text variant='body-2' color='neutral-faded'>
              {view.lastSyncedAt
                ? `Last pass completed ${instantFormat.format(timestampDate(view.lastSyncedAt))}.`
                : 'No pass has completed yet.'}
            </Text>
          </div>
        ) : (
          <div {...stylex.props(scimStyles.stack)}>
            <Text variant='body-2' color='neutral-faded'>
              No provisioning target is attached to this client.
            </Text>
            <Can permission='scim_provider:*:create'>
              <Button size='sm' onClick={() => setDialogOpen(true)}>
                Attach provider
              </Button>
            </Can>
          </div>
        )}

        {syncOutcome ? (
          <Text render={<p />} variant='body-2' color='primary'>
            {syncOutcome}
          </Text>
        ) : null}
        {refusal ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {refusal}
          </Text>
        ) : null}
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        ) : null}
      </CardContent>

      <ProviderDialog
        clientId={clientId}
        provider={unconfigured ? null : (view ?? null)}
        open={dialogOpen}
        onOpenChange={(next) => {
          if (!next) setDialogOpen(false)
        }}
        onDone={() => {
          setDialogOpen(false)
          void invalidate()
        }}
      />
    </Card>
  )
}

function DeleteProviderButton({ providerId }: { providerId: string }) {
  const queryClient = useQueryClient()
  const [failed, setFailed] = useState<string | null>(null)
  const removing = useMutation(ScimProviderService.method.delete, {
    onSuccess: () => {
      setFailed(null)
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: ScimProviderService.method.getByClient,
          cardinality: 'finite'
        })
      })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <>
      <AlertDialog>
        <AlertDialogTrigger render={<Button variant='destructive' size='sm' />}>
          Detach
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Detach the provisioning target?</AlertDialogTitle>
            <AlertDialogDescription>
              The data the sync pushed stays on the remote; ending the row only ends the sync.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                removing.mutate(create(DeleteScimProviderRequestSchema, { id: providerId }))
              }
            >
              Detach
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {failed ? (
        <Text render={<p />} variant='body-2' color='critical'>
          {failed}
        </Text>
      ) : null}
    </>
  )
}

function ProviderDialog({
  clientId,
  provider,
  open,
  onOpenChange,
  onDone
}: {
  clientId: string
  provider: ScimProvider | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  const [failed, setFailed] = useState<string | null>(null)
  const [endpoint, setEndpoint] = useState(provider?.endpoint ?? '')
  const [token, setToken] = useState('')
  const [attached, setAttached] = useState<string | null>(null)

  // The form is a scratch pad: opening it seeds from the provider as it
  // stands, so an attach starts empty and an update starts from the row.
  useEffect(() => {
    if (open) {
      setEndpoint(provider?.endpoint ?? '')
      setToken('')
    }
  }, [open, provider])

  const creating = useMutation(ScimProviderService.method.create, {
    onSuccess: (response) => {
      setFailed(null)
      // The token's one read: the show-once surface, then it is sealed.
      setAttached(response.provider?.token ?? '')
      onDone()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })
  const updating = useMutation(ScimProviderService.method.update, {
    onSuccess: () => {
      setFailed(null)
      onDone()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const submit = () => {
    if (!/^https?:\/\//.test(endpoint.trim())) {
      setFailed('The endpoint is an http(s) URL — the SCIM base address, `https://…/scim/v2`.')
      return
    }
    if (provider) {
      updating.mutate(
        create(UpdateScimProviderRequestSchema, {
          id: provider.id,
          endpoint: endpoint.trim(),
          token
        })
      )
    } else {
      creating.mutate(
        create(CreateScimProviderRequestSchema, { clientId, endpoint: endpoint.trim(), token })
      )
    }
  }

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!next) {
            setFailed(null)
            setToken('')
            setEndpoint(provider?.endpoint ?? '')
          }
          onOpenChange(next)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {provider ? 'Update the provisioning target' : 'Attach a provisioning target'}
            </DialogTitle>
            <DialogDescription>
              {provider
                ? 'An empty token keeps the stored one — a rotation is a value the caller supplies.'
                : 'The target answers the sync at its SCIM base URL; the token is its bearer secret.'}
            </DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel>Endpoint</FieldLabel>
            <Input
              value={endpoint}
              onChange={(event) => setEndpoint(event.target.value)}
              placeholder='https://target.example.com/scim/v2'
            />
          </Field>
          <Field>
            <FieldLabel>Bearer token</FieldLabel>
            <Input
              value={token}
              onChange={(event) => setToken(event.target.value)}
              placeholder={
                provider ? 'Keeps the stored token' : 'Empty — the target answers unauthenticated'
              }
            />
            <FieldDescription>Shown once at attach, then sealed at rest.</FieldDescription>
          </Field>
          {failed ? (
            <Text render={<p />} variant='body-2' color='critical'>
              {failed}
            </Text>
          ) : null}
          <DialogFooter>
            <Button variant='outline' onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button disabled={creating.isPending || updating.isPending} onClick={submit}>
              {provider ? 'Save target' : 'Attach'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <ShowOnceSecretDialog
        open={attached !== null}
        title='The SCIM bearer token'
        description='The sync presents this at the target. It is sealed at rest and never read back.'
        secret={attached ?? ''}
        onOpenChange={(next) => {
          if (!next) setAttached(null)
        }}
      />
    </>
  )
}

const scimStyles = stylex.create({
  row: {
    display: 'flex',
    alignItems: 'center',
    gap: 8
  },
  stackTight: {
    display: 'flex',
    flexDirection: 'column',
    gap: 4
  },
  stack: {
    display: 'flex',
    flexDirection: 'column',
    gap: 8
  },
  mono: {
    fontFamily: fontFamily.monospace,
    fontSize: 12
  }
})
