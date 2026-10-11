import { timestampFromDate } from '@bufbuild/protobuf/wkt'
import { useMutation } from '@connectrpc/connect-query'
import { createConnectQueryKey } from '@connectrpc/connect-query'
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue
} from 'uilibs/components/base/select'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { Textarea } from 'uilibs/components/extra/textarea'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { ApiKeyService } from '~/codegen/api_key_pb'

/** The windows the create dialog offers. Renewal answers the same scale —
 * three months for a routine key, a year for the ones a pipeline owns. */
const WINDOWS = [
  { value: '7d', label: '7 days', days: 7 },
  { value: '30d', label: '30 days', days: 30 },
  { value: '90d', label: '90 days', days: 90 },
  { value: '365d', label: '1 year', days: 365 }
] as const

type WindowValue = (typeof WINDOWS)[number]['value']

/**
 * The issue dialog (Wave 5 phase 5): a unique label, an optional note, and
 * the expiry window. The answer's raw key rides `onMinted` — the view owns
 * the show-once dialog, the dialog owns nothing after the close.
 */
export function ApiKeyCreateDialog({
  open,
  onOpenChange,
  onMinted
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onMinted: (rawKey: string) => void
}) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [windowValue, setWindowValue] = useState<WindowValue>('30d')
  const [failed, setFailed] = useState<string | null>(null)

  const queryClient = useQueryClient()

  const create = useMutation(ApiKeyService.method.createAPIKey, {
    onSuccess: (response) => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: ApiKeyService.method.listAPIKeys,
          cardinality: 'finite'
        })
      })
      setName('')
      setDescription('')
      setWindowValue('30d')
      setFailed(null)
      onOpenChange(false)
      onMinted(response.key)
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const nameValid = /^[a-zA-Z0-9_ -]{3,64}$/.test(name)
  const canSubmit = nameValid && !create.isPending

  const submit = () => {
    setFailed(null)
    const window = WINDOWS.find((one) => one.value === windowValue) ?? WINDOWS[1]
    const expiresAt = new Date()
    expiresAt.setDate(expiresAt.getDate() + window.days)
    create.mutate({
      name,
      description: description.trim() === '' ? undefined : description.trim(),
      expiresAt: timestampFromDate(expiresAt)
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Issue an API key</DialogTitle>
          <DialogDescription>
            The key acts as this account until it expires. The raw key appears once, right after
            issuing.
          </DialogDescription>
        </DialogHeader>

        <Field>
          <FieldLabel htmlFor='api-key-name'>Name</FieldLabel>
          <Input
            id='api-key-name'
            value={name}
            maxLength={64}
            placeholder='nightly-backup'
            onChange={(event) => setName(event.target.value)}
          />
          <FieldDescription>
            3–64 characters: letters, digits, underscores, dashes, spaces. Unique among your keys.
          </FieldDescription>
        </Field>

        <Field>
          <FieldLabel htmlFor='api-key-description'>Description</FieldLabel>
          <Textarea
            id='api-key-description'
            value={description}
            maxLength={255}
            rows={2}
            placeholder='What this key is for, so the audit trail reads plainly.'
            onChange={(event) => setDescription(event.target.value)}
          />
        </Field>

        <Field>
          <FieldLabel>Expires in</FieldLabel>
          <Select
            value={windowValue}
            onValueChange={(next) => {
              if (typeof next === 'string' && WINDOWS.some((one) => one.value === next)) {
                setWindowValue(next)
              }
            }}
          >
            <SelectTrigger aria-label='Expiry window'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {WINDOWS.map((one) => (
                <SelectItem key={one.value} value={one.value}>
                  {one.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <FieldDescription>
            An expired key authenticates nothing; renewal replaces its secret and window.
          </FieldDescription>
        </Field>

        {failed !== null && (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        )}

        <DialogFooter>
          <Button variant='ghost' onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={!canSubmit} onClick={submit}>
            {create.isPending ? <Spinner /> : 'Issue key'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
