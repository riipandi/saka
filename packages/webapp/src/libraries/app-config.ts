import { z } from 'zod'
import { fetcher } from './api-client'

/**
 * The deployment document `GET /api/configuration` answers: the public
 * subset to an anonymous caller, every non-secret setting to an
 * administrator. The wire keys are the backend's own (`omitzero` — an unset
 * or false value is omitted, hence the defaults), and the admin-scope
 * sections are stripped by the parse.
 */
const envelopeSchema = z.object({
  status: z.literal('success'),
  data: z.unknown()
})

export const appConfigSchema = z.object({
  app: z
    .object({
      mode: z.string().default(''),
      base_url: z.string().default(''),
      timezone: z.string().default('')
    })
    // `prefault`, not `default`: a missing section must still flow through
    // the leaf defaults, not land as a bare `{}`.
    .prefault({}),
  auth: z
    .object({
      one_time_access_email_as_admin_enabled: z.boolean().default(false),
      one_time_access_email_as_unauthenticated_enabled: z.boolean().default(false)
    })
    .prefault({}),
  oidc: z.object({ enabled: z.boolean().default(false) }).prefault({}),
  mailer: z
    .object({
      notifications: z
        .object({ announcement_email_enabled: z.boolean().default(false) })
        .prefault({})
    })
    .prefault({})
})

export type AppConfig = z.infer<typeof appConfigSchema>

/** Fetch and parse the configuration document; throws on a malformed body. */
export async function readAppConfig(): Promise<AppConfig> {
  const envelope = envelopeSchema.parse(await fetcher('configuration'))
  return appConfigSchema.parse(envelope.data)
}
