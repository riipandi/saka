import { useSelector } from '@tanstack/react-store'
import { authStore } from './auth-store'
import { grantsSatisfy } from './permissions'

/**
 * The store-backed permission read for components: the same snapshot the
 * synchronous `can()` answers, subscribed so a custody change re-renders the
 * reader. The `can` closure is remade per render and carries no state — the
 * snapshot it closes over is the one this render read.
 */
export function usePermissions(): {
  can: (requirement: string) => boolean
  isAdministrator: boolean
} {
  const grants = useSelector(authStore, (state) => state.grants)

  return {
    can: (requirement: string) => grantsSatisfy(grants.permissions, requirement),
    isAdministrator: grants.roles.includes('administrator')
  }
}
