import { useCallback, useMemo, useState } from 'react'
import { StepUpCancelled, createStepUpRunner } from './reauth'

interface PendingChallenge {
  resolve: (token: string) => void
  reject: (reason: unknown) => void
}

/**
 * The view-side step-up: `run` opens the Reauthenticate dialog, and the
 * proof the caller proves with is spent on exactly the call the view handed
 * in — no token is stored anywhere, the closure is its only holder.
 *
 * The guarded action's own error surfaces to the view; a dismissal rejects
 * with `StepUpCancelled`, which the view swallows rather than reads as a
 * failure the user caused by backing out of a dialog.
 */
export function useStepUp() {
  const [pending, setPending] = useState<PendingChallenge | null>(null)

  const challenge = useCallback(
    () =>
      new Promise<string>((resolve, reject) => {
        setPending({ resolve, reject })
      }),
    []
  )

  const run = useMemo(() => createStepUpRunner(challenge), [challenge])

  const prove = useCallback(
    (token: string) => {
      pending?.resolve(token)
      setPending(null)
    },
    [pending]
  )

  const dismiss = useCallback(() => {
    pending?.reject(new StepUpCancelled())
    setPending(null)
  }, [pending])

  return {
    run,
    dialogOpen: pending !== null,
    onProven: prove,
    onDismissed: dismiss
  }
}
