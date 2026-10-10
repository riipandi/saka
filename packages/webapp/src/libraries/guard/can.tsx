import type { ReactNode } from 'react'
import { usePermissions } from './use-permissions'

interface CanProps {
  /**
   * The requirement the render asks for — a full slug,
   * `resource:instance:action`, the wildcard instance when it is written for
   * the kind (`user:*:read`). A malformed slug answers false in the matcher,
   * which here means the surface silently disappears; naming the mistake at
   * the render is the cheaper failure.
   */
  permission: string
  /** Rendered instead of the children when the requirement is unmet. */
  fallback?: ReactNode
  children?: ReactNode
}

/**
 * The render gate over the grant snapshot: children when the requirement is
 * met, the fallback or nothing otherwise. It hides, it never disables — a
 * control the snapshot does not admit is not offered, and a missed case
 * degrades to the server's refusal word, never a silent lie.
 */
export function Can({ permission, fallback, children }: CanProps) {
  if (permission.split(':').length !== 3) {
    console.warn(`Can: ${permission} is not a resource:instance:action slug`)
  }

  const { can } = usePermissions()
  if (can(permission)) return <>{children}</>
  return fallback != null ? <>{fallback}</> : null
}
