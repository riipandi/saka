import { useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { Spinner } from 'uilibs/components/extra/spinner'
import {
  clearSignInPause,
  readSignInPause,
  type SignInPause as StoredPause
} from '#/libraries/guard/sign-in-pause'
import { MfaChallengeView } from './-mfa-challenge-view'
import { MfaEnrollmentView } from './-mfa-enrollment-view'

/** The pause page's search: the login page's own schema, passthrough —
 * extra params ride along, and the typed members are what the restart
 * navigation needs. */
interface PauseSearch { [key: string]: unknown; return_to?: string }

/**
 * The pause page: the sign-in's multi-factor fork rendered from the bridge
 * the pause slot holds. A refresh restores the state — the bridge and the
 * return destination live in sessionStorage, and the bridge's own expiry is
 * the clock — while a visit without a live pause (first load, a spent
 * bridge, the tab's storage gone) walks back to the credentials form with
 * the search intact.
 */
export function MfaPauseView({ search }: { search: PauseSearch }) {
  const navigate = useNavigate()
  const [pause, setPause] = useState<StoredPause | null>(null)

  useEffect(() => {
    const read = readSignInPause()
    if (!read) {
      void navigate({ to: '/login', search, replace: true })
      return
    }
    setPause(read)
    // The search object is the route's own parse — stable per navigation,
    // and the read runs once per mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const restartSignIn = () => {
    clearSignInPause()
    void navigate({ to: '/login', search, replace: true })
  }

  if (!pause) {
    return (
      <div style={{ display: 'grid', placeItems: 'center', minHeight: '50vh' }}>
        <Spinner />
      </div>
    )
  }

  if (pause.challenge.kind === 'mfa-challenge') {
    return (
      <MfaChallengeView
        challenge={pause.challenge}
        returnTo={pause.returnTo}
        onRestart={restartSignIn}
      />
    )
  }

  return (
    <MfaEnrollmentView
      challenge={pause.challenge}
      returnTo={pause.returnTo}
      onRestart={restartSignIn}
    />
  )
}
