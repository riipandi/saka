import { Check, Copy } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
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
import { Input } from 'uilibs/components/base/input'
import { Text } from 'uilibs/components/extra/text'

/**
 * The credential the server shows once: minted client secrets, SCIM tokens,
 * and the other raw values whose stored form is a hash. The dialog is the
 * value's whole display surface — copy first, acknowledge, and the value is
 * gone for good, because no procedure can read it back.
 */
export function ShowOnceSecretDialog({
  open,
  onOpenChange,
  title,
  description,
  secret,
  secretLabel = 'Secret',
  acknowledgeLabel = 'I have stored it'
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: string
  secret: string
  secretLabel?: string
  acknowledgeLabel?: string
}) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    await navigator.clipboard.writeText(secret)
    setCopied(true)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        // Leaving the dialog is leaving the value: the state resets so a
        // re-open can never replay a stale secret.
        if (!next) setCopied(false)
        onOpenChange(next)
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <div {...stylex.props(secretStyles.row)}>
          <Input readOnly value={secret} aria-label={secretLabel} />
          <Button variant='outline' size='sm' onClick={() => void copy()}>
            {copied ? <Check size={16} /> : <Copy size={16} />}
            {copied ? 'Copied' : 'Copy'}
          </Button>
        </div>
        <Text render={<p />} variant='body-2' color='critical'>
          This value is shown only once. Store it now — it cannot be read back.
        </Text>
        <DialogFooter>
          {/* Closing is the acknowledgement: the onOpenChange contract drops
          the state that held the value. */}
          <Button variant='primary' onClick={() => onOpenChange(false)}>
            {acknowledgeLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

const secretStyles = stylex.create({
  row: {
    display: 'flex',
    alignItems: 'center',
    gap: 8
  }
})
