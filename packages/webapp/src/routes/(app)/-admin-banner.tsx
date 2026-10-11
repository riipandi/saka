import { UserCog } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Text } from 'uilibs/components/extra/text'
import { useAuthentication, useAuthGrants, useAuthUser } from '#/hooks/use-auth'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { styles } from '#/styles/element/admin-banner.stylex'

/**
 * The impersonation banner. The access token's actor pair is the whole
 * signal that the session is a delegation — the banner renders when
 * `actor_id` is present, names the borrowed account and the administrator
 * behind it, and offers the way out. The username claim does not survive a
 * renewal, so the actor's id is the fact the sentence falls back to.
 */
export function AdminBanner() {
  const user = useAuthUser()
  const grants = useAuthGrants()
  const { stopImpersonation } = useAuthentication()
  const [failed, setFailed] = useState<string | null>(null)

  if (!grants.actorId) return null

  const acting = user?.username?.trim() || user?.email || 'the borrowed account'
  const actor = grants.actorUsername?.trim() || grants.actorId

  const stop = async () => {
    setFailed(null)
    try {
      await stopImpersonation()
    } catch (error) {
      setFailed(getErrorMessage(error))
    }
  }

  return (
    <div {...stylex.props(styles.banner)} data-testid='impersonation-banner'>
      <span {...stylex.props(styles.label)}>
        <UserCog size={18} strokeWidth={1.8} />
        <Text render={<span />} variant='caption-1' weight='medium' color='warning'>
          Acting as {acting} — impersonated by {actor}
        </Text>
      </span>
      <span {...stylex.props(styles.grow)} />
      <Button
        type='button'
        variant='secondary'
        size='sm'
        onClick={() => void stop()}
        disabled={failed != null}
      >
        Stop impersonating
      </Button>
      {failed && (
        <Text render={<span />} variant='caption-1' color='critical'>
          {failed}
        </Text>
      )}
    </div>
  )
}
