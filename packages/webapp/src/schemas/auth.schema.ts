import { z } from 'zod'

/**
 * Frontend-only form validation. The wire contract lives in
 * `~/codegen/proto/ts/authn_pb` (saka.authn.v1) — this schema never models a
 * response, only what the sign-in form collects.
 */
export const loginSchema = z.object({
  username: z.string().min(1, { error: 'Username is required' }),
  password: z.string().min(1, { error: 'Password is required' })
})

/** Login request payload — single source of truth is the validation schema. */
export type LoginCredentials = z.infer<typeof loginSchema>
