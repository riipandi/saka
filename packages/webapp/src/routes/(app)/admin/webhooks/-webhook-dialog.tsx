import { useMutation, useQuery } from '@connectrpc/connect-query'
import { createConnectQueryKey } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Checkbox } from 'uilibs/components/base/checkbox'
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
import { WebhookService } from '~/codegen/webhook_pb'

const METHODS = ['POST', 'PUT', 'PATCH', 'GET', 'DELETE'] as const

/**
 * The registration dialog (Wave 5 phase 4): a destination URL, a method,
 * optional custom headers, and the events it subscribes to — the catalog
 * `ListEventTypes` answers, or the `*` wildcard, or an empty list for
 * every event. The create answer's secret rides `onMinted` to the page's
 * show-once dialog.
 */
export function WebhookCreateDialog({
  open,
  onOpenChange,
  onMinted
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onMinted: (secret: string) => void
}) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [endpoint, setEndpoint] = useState('')
  const [method, setMethod] = useState<(typeof METHODS)[number]>('POST')
  const [headersText, setHeadersText] = useState('')
  const [selectedEvents, setSelectedEvents] = useState<string[]>([])
  const [failed, setFailed] = useState<string | null>(null)

  const queryClient = useQueryClient()

  const catalog = useQuery(
    WebhookService.method.listEventTypes,
    {},
    { enabled: open, retry: false }
  )

  const create = useMutation(WebhookService.method.create, {
    onSuccess: (response) => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: WebhookService.method.list,
          cardinality: 'finite'
        })
      })
      onMinted(response.secret)
      close(false)
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const close = (next: boolean) => {
    if (!next) {
      setName('')
      setDescription('')
      setEndpoint('')
      setMethod('POST')
      setHeadersText('')
      setSelectedEvents([])
      setFailed(null)
    }
    onOpenChange(next)
  }

  const headers = parseHeaders(headersText)
  const headersValid = headers !== null

  const canSubmit =
    name.trim().length >= 3 &&
    endpoint.trim().startsWith('http') &&
    headersValid &&
    !create.isPending

  const submit = () => {
    setFailed(null)
    create.mutate({
      name: name.trim(),
      description: description.trim() === '' ? undefined : description.trim(),
      endpoint: endpoint.trim(),
      method,
      headers: headers ?? {},
      eventTypes: selectedEvents
    })
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Register an endpoint</DialogTitle>
          <DialogDescription>
            The destination the deployment's matching events go to, signed with the secret this
            registration mints.
          </DialogDescription>
        </DialogHeader>

        <Field>
          <FieldLabel htmlFor='webhook-name'>Name</FieldLabel>
          <Input
            id='webhook-name'
            value={name}
            maxLength={100}
            placeholder='billing-bridge'
            onChange={(event) => setName(event.target.value)}
          />
          <FieldDescription>ASCII letters, digits, dashes, and underscores.</FieldDescription>
        </Field>

        <Field>
          <FieldLabel htmlFor='webhook-description'>Description</FieldLabel>
          <Textarea
            id='webhook-description'
            value={description}
            maxLength={255}
            rows={2}
            onChange={(event) => setDescription(event.target.value)}
          />
        </Field>

        <Field>
          <FieldLabel htmlFor='webhook-endpoint'>Endpoint URL</FieldLabel>
          <Input
            id='webhook-endpoint'
            type='url'
            value={endpoint}
            placeholder='https://example.com/hooks/saka'
            onChange={(event) => setEndpoint(event.target.value)}
          />
        </Field>

        <Field>
          <FieldLabel>Method</FieldLabel>
          <Select
            value={method}
            onValueChange={(next) => {
              if (typeof next === 'string' && (METHODS as readonly string[]).includes(next)) {
                setMethod(next)
              }
            }}
          >
            <SelectTrigger aria-label='Method'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {METHODS.map((one) => (
                <SelectItem key={one} value={one}>
                  {one}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>

        <Field>
          <FieldLabel htmlFor='webhook-headers'>Custom headers</FieldLabel>
          <Textarea
            id='webhook-headers'
            value={headersText}
            rows={2}
            placeholder={'X-Team: billing\nX-Env: production'}
            onChange={(event) => setHeadersText(event.target.value)}
          />
          <FieldDescription>
            One per line as Name: value. They cannot override the signature set.
          </FieldDescription>
          {!headersValid && (
            <Text render={<p />} variant='body-2' color='critical'>
              Each line must read Name: value.
            </Text>
          )}
        </Field>

        <Field>
          <FieldLabel>Events</FieldLabel>
          {catalog.isPending ? (
            <Spinner />
          ) : catalog.isError ? (
            <Text variant='body-2' color='critical'>
              {getErrorMessage(catalog.error)}
            </Text>
          ) : (
            <div role='group' aria-label='Event subscriptions' {...stylex.props(styles.eventList)}>
              {(catalog.data?.eventTypes ?? []).map((entry) => {
                const checked = selectedEvents.includes(entry.name)
                return (
                  <label key={entry.name} {...stylex.props(styles.eventRow)}>
                    <Checkbox
                      checked={checked}
                      onCheckedChange={(next) =>
                        setSelectedEvents((current) => {
                          if (next) return [...current, entry.name]
                          return current.filter((one) => one !== entry.name)
                        })
                      }
                      aria-label={entry.name}
                    />
                    <span {...stylex.props(styles.eventText)}>
                      <span>{entry.name}</span>
                      <span {...stylex.props(styles.eventHint)}>{entry.description}</span>
                    </span>
                  </label>
                )
              })}
            </div>
          )}
          <FieldDescription>
            An empty selection receives every event; the wildcard '*' does the same.
          </FieldDescription>
        </Field>

        {failed !== null && (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        )}

        <DialogFooter>
          <Button variant='ghost' onClick={() => close(false)}>
            Close
          </Button>
          <Button disabled={!canSubmit} onClick={submit}>
            {create.isPending ? <Spinner /> : 'Register'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** Parse the "Name: value" textarea into the wire's header map; null is the
 * malformed answer the form refuses. */
function parseHeaders(text: string): Record<string, string> | null {
  if (text.trim() === '') return {}
  const headers: Record<string, string> = {}
  for (const line of text.split('\n')) {
    if (line.trim() === '') continue
    const separator = line.indexOf(':')
    if (separator <= 0) return null
    const key = line.slice(0, separator).trim()
    const value = line.slice(separator + 1).trim()
    if (key === '' || value === '') return null
    headers[key] = value
  }
  return headers
}

const styles = stylex.create({
  eventList: {
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
    maxHeight: 200,
    overflowY: 'auto'
  },
  eventRow: {
    display: 'flex',
    alignItems: 'flex-start',
    gap: 10,
    cursor: 'pointer'
  },
  eventText: {
    display: 'flex',
    flexDirection: 'column',
    gap: 2
  },
  eventHint: {
    fontSize: 12,
    opacity: 0.7
  }
})
