/**
 * The permission matcher, ported segment for segment from
 * `framework/authz/grants.go` — the Go table in `framework/authz/
 * authz_test.go` is this file's test contract. The UI and the server must
 * judge by the same rule, so a requirement written for the guard reads as a
 * literal here; a port that answers differently is a defect, not an
 * improvement.
 */

/** The instance segment that stands for every instance of a resource. */
export const WILDCARD = '*'

/**
 * Whether a held grant satisfies a requirement. Both are full slugs,
 * `resource:instance:action`: the first and third segments compare exactly,
 * and the grant's middle segment answers the requirement's own or the
 * wildcard. The wildcard is the instance position's alone — `user:usr_1:*`
 * and `*:*:read` are not grants — and a grant the catalog does not declare
 * still matches, because the catalog is the seed's input, not the matcher's
 * gate.
 */
export function matchRequirement(requirement: string, grant: string): boolean {
  const required = requirement.split(':')
  const held = grant.split(':')
  if (required.length !== 3 || held.length !== 3) return false

  for (const part of [...required, ...held]) {
    if (part === '') return false
  }

  if (required[0] !== held[0] || required[2] !== held[2]) return false
  return held[1] === WILDCARD || held[1] === required[1]
}

/**
 * Whether the held set satisfies the requirement: one matching grant is
 * enough, the empty set satisfies nothing.
 */
export function grantsSatisfy(held: readonly string[], requirement: string): boolean {
  return held.some((grant) => matchRequirement(requirement, grant))
}
