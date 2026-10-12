import { Badge } from 'uilibs/components/extra/badge'

/**
 * The scope words a consent carries, worded for the reader: the offline
 * scope is a persistent access the app keeps renewing, and the login hint
 * scope only ever prefills a form. Every consent surface — the live
 * question on the interaction page and the two ledgers — renders the same
 * chips, so a scope never changes meaning between screens.
 */
export function ScopeChip({ scope }: { scope: string }) {
  if (scope === 'offline_access') {
    return <Badge variant='primary'>offline_access — persistent access</Badge>
  }
  if (scope === 'login_hint') {
    return <Badge variant='secondary'>login_hint — prefills the sign-in form</Badge>
  }
  return <Badge variant='outline'>{scope}</Badge>
}
