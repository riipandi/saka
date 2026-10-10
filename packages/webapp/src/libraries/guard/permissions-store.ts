import { authStore } from './auth-store'
import { grantsSatisfy } from './permissions'

/**
 * The system role the backend seeds the administrator account with — the
 * value `internal/authz/catalog.go` declares. The frontend spells it once,
 * here, so no other file repeats the string.
 */
const ADMINISTRATOR_ROLE = 'administrator'

/**
 * Whether the signed-in account's grant snapshot satisfies the requirement —
 * a synchronous read, deliberate: route guards and request interceptors ask
 * outside React, and the answer must not depend on a component being
 * mounted. The requirement is a full slug, `resource:instance:action`, the
 * wildcard instance when it is written for the kind.
 */
export function can(requirement: string): boolean {
  return grantsSatisfy(authStore.state.grants.permissions, requirement)
}

/** Whether the snapshot names the administrator system role. */
export function isAdministrator(): boolean {
  return authStore.state.grants.roles.includes(ADMINISTRATOR_ROLE)
}
