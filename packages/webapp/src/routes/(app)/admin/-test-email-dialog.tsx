import { useMutation } from '@connectrpc/connect-query'
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
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { AppConfigService } from '~/codegen/system_pb'

/**
 * The mailer probe (Wave 5 phase 1): one rendered message through the
 * deployment's mailer, to the caller on record or an override address —
 * the synchronous answer says rendered and submitted, nothing more.
 */
export function TestEmailDialog({
  open,
  onOpenChange
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [to, setTo] = useState('')
  const [failed, setFailed] = useState<string | null>(null)
  const [sent, setSent] = useState<string | null>(null)

  const testEmail = useMutation(AppConfigService.method.testEmail, {
    onSuccess: (response) => {
      setFailed(null)
      setSent(response.message)
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const close = (next: boolean) => {
    if (!next) {
      setSent(null)
      setFailed(null)
      setTo('')
    }
    onOpenChange(next)
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Send a test email</DialogTitle>
          <DialogDescription>
            One message through the deployment's mailer. Leave the address empty to send it to your
            own account.
          </DialogDescription>
        </DialogHeader>

        <Field>
          <FieldLabel>Recipient override</FieldLabel>
          <Input
            type='email'
            value={to}
            placeholder='leave empty for your own address'
            onChange={(event) => setTo(event.target.value)}
          />
          <FieldDescription>
            Acceptance is the mailer's word — check the target mailbox for the arrival.
          </FieldDescription>
        </Field>

        {sent !== null && (
          <Text render={<p />} variant='body-2'>
            {sent}
          </Text>
        )}
        {failed !== null && (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        )}

        <DialogFooter>
          <Button variant='ghost' onClick={() => close(false)}>
            Close
          </Button>
          <Button
            disabled={testEmail.isPending}
            onClick={() => {
              setFailed(null)
              setSent(null)
              testEmail.mutate({ to: to === '' ? undefined : to })
            }}
          >
            {testEmail.isPending ? <Spinner /> : 'Send'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
