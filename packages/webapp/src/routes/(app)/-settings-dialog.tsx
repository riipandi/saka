import { Bin } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useState } from 'react'
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
import { Field, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'

const styles = stylex.create({
  error: {
    marginTop: 8
  }
})

/**
 * The typed confirmation the deletion demands: the dialog owns its open
 * state and the typed word, and the confirm button stays disabled until the
 * typed value matches — the account's handle is the one word the deleter
 * cannot mean by accident.
 */
export function DeleteAccountDialog({
  username,
  onDelete,
  pending,
  error
}: {
  username: string
  onDelete: () => void
  pending: boolean
  error: string | null
}) {
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState('')
  return (
    <AlertDialog
      open={open}
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen)
        if (!nextOpen) setTyped('')
      }}
    >
      <AlertDialogTrigger render={<Button variant='destructive' />}>
        <Bin size={16} />
        Delete account
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete this account?</AlertDialogTitle>
          <AlertDialogDescription>
            This removes the account permanently — its sessions end and its profile picture is
            discarded. Type the username to confirm.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <Field>
          <FieldLabel htmlFor='delete-account-confirm'>Username</FieldLabel>
          <Input
            id='delete-account-confirm'
            value={typed}
            placeholder={username}
            autoComplete='off'
            onChange={(event) => setTyped(event.target.value)}
          />
          {error ? (
            <Text render={<p />} variant='body-2' color='critical' style={styles.error}>
              {error}
            </Text>
          ) : null}
        </Field>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant='destructive'
            disabled={typed !== username || pending}
            onClick={() => onDelete()}
          >
            {pending ? <Spinner /> : <Bin size={16} />}
            Delete account
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
