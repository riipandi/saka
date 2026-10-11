import { create } from '@bufbuild/protobuf'
import { timestampFromDate } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation } from '@connectrpc/connect-query'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
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
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { DateTimePicker } from '#/components/-date-time-picker'
import { ShowOnceSecretDialog } from '#/components/dialogs/-show-once-secret-dialog'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { CreateOidcClientSecretRequestSchema, OidcClientService } from '~/codegen/federation_pb'

/**
 * The minting dialog: one more secret, drawn by the server or typed by the
 * operator, with an optional expiry window. Its answer is the show-once
 * surface — the raw value exists only until the acknowledgement closes it.
 */
export function MintSecretDialog({ clientId }: { clientId: string }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [expiresAt, setExpiresAt] = useState<Date | undefined>(undefined)
  const [custom, setCustom] = useState('')
  const [minted, setMinted] = useState<string | null>(null)

  const minting = useMutation(OidcClientService.method.createSecret, {
    onSuccess: (response) => {
      setFailed(null)
      setMinted(response.value)
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: OidcClientService.method.listSecrets,
          cardinality: 'finite'
        })
      })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const enter = (next: boolean) => {
    setOpen(next)
    if (next) {
      setFailed(null)
      setMinted(null)
      setExpiresAt(undefined)
      setCustom('')
    }
  }

  const submit = () => {
    const when = expiresAt && !Number.isNaN(expiresAt.getTime()) ? expiresAt : undefined
    minting.mutate(
      create(CreateOidcClientSecretRequestSchema, {
        id: clientId,
        secret: custom.trim() ? custom.trim() : undefined,
        expiresAt: when ? timestampFromDate(when) : undefined
      })
    )
  }

  return (
    <>
      <Button variant='outline' size='sm' onClick={() => enter(true)}>
        Mint secret
      </Button>
      <Dialog open={open} onOpenChange={enter}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Mint a client secret</DialogTitle>
            <DialogDescription>
              Several live secrets are legitimate — a rotation is an addition followed by a
              withdrawal, never a swap.
            </DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel>Expires at</FieldLabel>
            <DateTimePicker
              value={expiresAt}
              onChange={setExpiresAt}
              ariaLabel='Expires at'
              placeholder='Empty — the secret never expires'
            />
          </Field>
          <Field>
            <FieldLabel>Custom value</FieldLabel>
            <Input
              value={custom}
              onChange={(event) => setCustom(event.target.value)}
              placeholder='Drawn by the server when left empty'
              pattern='[a-zA-Z0-9]{16,128}'
            />
            <FieldDescription>
              Re-issue a credential the operator already holds — at least 16 alphanumeric
              characters.
            </FieldDescription>
          </Field>
          {failed ? (
            <Text render={<p />} variant='body-2' color='critical'>
              {failed}
            </Text>
          ) : null}
          <DialogFooter>
            <Button variant='outline' onClick={() => enter(false)}>
              Cancel
            </Button>
            <Button disabled={minting.isPending} onClick={submit}>
              {minting.isPending ? <Spinner /> : null}
              Mint
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <ShowOnceSecretDialog
        open={minted !== null}
        title='The new secret'
        description='The relying party authenticates with this value. It is stored as a hash alone.'
        secret={minted ?? ''}
        onOpenChange={(next) => {
          if (!next) {
            setMinted(null)
            enter(false)
          }
        }}
      />
    </>
  )
}
