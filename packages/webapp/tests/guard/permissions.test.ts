import { describe, expect, it } from 'vite-plus/test'
import { grantsSatisfy, matchRequirement } from '#/libraries/guard/permissions'

/**
 * The table is `framework/authz/authz_test.go` `TestMatchHonorsTheWildcard
 * OnlyInTheInstancePosition` transcribed pair for pair — the two matchers
 * must answer identically, so a pair added on either side rides here too.
 */
const matchTable: Array<{ name: string; requirement: string; grant: string; want: boolean }> = [
  { name: 'exact grant', requirement: 'user:usr_1:read', grant: 'user:usr_1:read', want: true },
  { name: 'wildcard instance', requirement: 'user:usr_1:read', grant: 'user:*:read', want: true },
  {
    name: 'different instance',
    requirement: 'user:usr_1:read',
    grant: 'user:usr_2:read',
    want: false
  },
  {
    name: 'different resource',
    requirement: 'user:usr_1:read',
    grant: 'session:usr_1:read',
    want: false
  },
  {
    name: 'different action',
    requirement: 'user:usr_1:read',
    grant: 'user:usr_1:update',
    want: false
  },
  {
    name: 'wildcard action is not a grant',
    requirement: 'user:usr_1:read',
    grant: 'user:usr_1:*',
    want: false
  },
  {
    name: 'wildcard resource is not a grant',
    requirement: 'user:usr_1:read',
    grant: '*:*:read',
    want: false
  },
  { name: 'two-part slug', requirement: 'user:read', grant: 'user:*:read', want: false },
  { name: 'empty segments', requirement: 'user::read', grant: 'user:*:read', want: false }
]

describe('the permission matcher', () => {
  it.each(matchTable)('$name', ({ requirement, grant, want }) => {
    expect(matchRequirement(requirement, grant)).toBe(want)
  })

  it('answers across the held set the way the Go Grants answers', () => {
    const held = ['notification:*:read', 'user:usr_1:ban']

    expect(grantsSatisfy(held, 'notification:ntf_9:read')).toBe(true)
    expect(grantsSatisfy(held, 'user:usr_1:ban')).toBe(true)
    expect(grantsSatisfy(held, 'user:usr_2:ban')).toBe(false)
    expect(grantsSatisfy([], 'user:*:read')).toBe(false)
  })

  it('satisfies an undeclared grant the same as the Go matcher does', () => {
    // The catalog is the seed's input, not the matcher's gate: a slug minted
    // beside a feature must keep working after a catalog entry is renamed
    // away under it.
    expect(matchRequirement('widget:wid_1:spin', 'widget:*:spin')).toBe(true)
  })
})
