import { LogOut } from '@keyline-icons/react'
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
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'

const styles = stylex.create({
  error: {
    marginTop: 8
  }
})

/**
 * The shared confirmation the two bulk sign-outs ride — distinct actions,
 * one shape: the trigger names the action, the confirm button repeats it,
 * and the server's refusal (a count the caller cannot know in advance, an
 * eviction the session carried) reads inside the dialog.
 */
export function SignOutDialog({
  trigger,
  title,
  description,
  pending,
  error,
  onConfirm
}: {
  trigger: string
  title: string
  description: string
  pending: boolean
  error: string | null
  onConfirm: () => void
}) {
  const [open, setOpen] = useState(false)
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger render={<Button variant='outline' size='sm' />}>
        {trigger}
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
          {error ? (
            <Text render={<p />} variant='body-2' color='critical' style={styles.error}>
              {error}
            </Text>
          ) : null}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant='destructive'
            disabled={pending}
            onClick={() => {
              setOpen(false)
              onConfirm()
            }}
          >
            {pending ? <Spinner /> : <LogOut size={16} />}
            {trigger}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
