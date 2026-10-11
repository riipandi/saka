import { Checkbox } from 'uilibs/components/base/checkbox'
import { Field, FieldDescription, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import { Text } from 'uilibs/components/extra/text'
import { Textarea } from 'uilibs/components/extra/textarea'
import type { OidcClient } from '~/codegen/federation_pb'

/**
 * The grant words the token endpoint serves. An empty list on the wire means
 * the registered default applies — the checkboxes start unchecked and the
 * copy says what leaving them all off means.
 */
export const GRANT_TYPES = [
  { value: 'authorization_code', label: 'Authorization code' },
  { value: 'refresh_token', label: 'Refresh token' },
  { value: 'urn:ietf:params:oauth:grant-type:device_code', label: 'Device code' },
  { value: 'client_credentials', label: 'Client credentials' }
] as const

/** The form's own shape — every wire field the create and edit forms share,
 * held as strings until the submit turns them into the request. */
export interface OidcClientFormState {
  id: string
  name: string
  description: string
  callbackUrls: string
  logoutCallbackUrls: string
  launchUrl: string
  isPublic: boolean
  pkceEnabled: boolean
  requiresReauthentication: boolean
  requiresPar: boolean
  skipConsent: boolean
  accessTokenDuration: string
  refreshTokenDuration: string
  backchannelLogoutUri: string
  backchannelSessionRequired: boolean
  grantTypes: string[]
}

export const emptyClientForm: OidcClientFormState = {
  id: '',
  name: '',
  description: '',
  callbackUrls: '',
  logoutCallbackUrls: '',
  launchUrl: '',
  isPublic: false,
  pkceEnabled: false,
  requiresReauthentication: false,
  requiresPar: false,
  skipConsent: false,
  accessTokenDuration: '',
  refreshTokenDuration: '',
  backchannelLogoutUri: '',
  backchannelSessionRequired: false,
  grantTypes: []
}

/** The edit form's seed: the client's fields as the view carries them. */
export function clientFormFromClient(client: OidcClient): OidcClientFormState {
  return {
    id: client.id,
    name: client.name,
    description: client.description ?? '',
    callbackUrls: client.callbackUrls.join('\n'),
    logoutCallbackUrls: client.logoutCallbackUrls.join('\n'),
    launchUrl: client.launchUrl ?? '',
    isPublic: client.isPublic,
    pkceEnabled: client.pkceEnabled,
    requiresReauthentication: client.requiresReauthentication,
    requiresPar: client.requiresPushedAuthorizationRequests,
    skipConsent: client.skipConsent,
    accessTokenDuration: client.accessTokenDurationMinutes
      ? String(client.accessTokenDurationMinutes)
      : '',
    refreshTokenDuration: client.refreshTokenDurationMinutes
      ? String(client.refreshTokenDurationMinutes)
      : '',
    backchannelLogoutUri: client.backchannelLogoutUri ?? '',
    backchannelSessionRequired: client.backchannelLogoutSessionRequired,
    grantTypes: [...client.allowedGrantTypes]
  }
}

/** The redirect URIs the textarea carries, one per line, blanks dropped. */
export function parseLines(value: string): string[] {
  return value
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
}

/** The form's pre-submit validation, in the order the fields render. The
 * wire's own validators are the last word; these catch the mistakes the
 * browser can name before the round trip spends them. */
export function validateClientForm(state: OidcClientFormState): string | null {
  if (!state.name.trim()) return 'The client needs a name.'
  const callbacks = parseLines(state.callbackUrls)
  if (callbacks.length === 0) return 'A client with no callback URL cannot receive a response.'
  for (const url of [...callbacks, ...parseLines(state.logoutCallbackUrls)]) {
    if (!isAbsoluteUrl(url)) return `Not an absolute URL: ${url}`
  }
  if (state.launchUrl && !isAbsoluteUrl(state.launchUrl)) return 'The launch URL must be absolute.'
  if (state.backchannelLogoutUri && !isAbsoluteUrl(state.backchannelLogoutUri)) {
    return 'The back-channel logout URI must be absolute.'
  }
  for (const key of ['accessTokenDuration', 'refreshTokenDuration'] as const) {
    if (state[key] && (!Number.isInteger(Number(state[key])) || Number(state[key]) < 1)) {
      return 'The token durations are whole minutes.'
    }
  }
  return null
}

function isAbsoluteUrl(value: string): boolean {
  try {
    // The wire's URI validator wants an absolute URI; the constructor is the
    // browser's own word for the same rule.
    return Boolean(new URL(value))
  } catch {
    return false
  }
}

/**
 * The fields the create and edit forms share. The id lives here for the
 * create form's optional identifier; the edit form hides it — a client's
 * id is the credential its relying party presents, not a rename target.
 */
export function OidcClientFields({
  state,
  onChange,
  mode
}: {
  state: OidcClientFormState
  onChange: (next: OidcClientFormState) => void
  mode: 'create' | 'edit'
}) {
  const set = (patch: Partial<OidcClientFormState>) => onChange({ ...state, ...patch })
  const toggleGrant = (value: string, on: boolean) =>
    set({
      grantTypes: on ? [...state.grantTypes, value] : state.grantTypes.filter((g) => g !== value)
    })

  return (
    <div className='flex flex-col gap-4'>
      {mode === 'create' ? (
        <Field>
          <FieldLabel>Client ID</FieldLabel>
          <Input
            value={state.id}
            onChange={(event) => set({ id: event.target.value })}
            placeholder='Generated when left empty'
            pattern='[a-zA-Z0-9_-]{1,64}'
          />
          <FieldDescription>
            The identifier the relying party presents — letters, digits, underscores, hyphens.
          </FieldDescription>
        </Field>
      ) : null}
      <Field>
        <FieldLabel>Name</FieldLabel>
        <Input value={state.name} onChange={(event) => set({ name: event.target.value })} />
        <FieldDescription>The display name a sign-in page shows.</FieldDescription>
      </Field>
      <Field>
        <FieldLabel>Description</FieldLabel>
        <Input
          value={state.description}
          onChange={(event) => set({ description: event.target.value })}
        />
      </Field>
      <Field>
        <FieldLabel>Callback URLs</FieldLabel>
        <Textarea
          value={state.callbackUrls}
          onChange={(event) => set({ callbackUrls: event.target.value })}
          placeholder={'https://app.example.com/callback\nhttps://app.example.com/oidc/callback'}
          aria-label='Callback URLs'
        />
        <FieldDescription>One redirect URI per line — the exact-match list.</FieldDescription>
      </Field>
      <Field>
        <FieldLabel>Logout callback URLs</FieldLabel>
        <Textarea
          value={state.logoutCallbackUrls}
          onChange={(event) => set({ logoutCallbackUrls: event.target.value })}
          aria-label='Logout callback URLs'
        />
        <FieldDescription>One post-logout redirect URI per line, optional.</FieldDescription>
      </Field>
      <Field>
        <FieldLabel>Launch URL</FieldLabel>
        <Input
          value={state.launchUrl}
          onChange={(event) => set({ launchUrl: event.target.value })}
          placeholder='https://app.example.com'
        />
        <FieldDescription>
          The address the console launches the client at, optional.
        </FieldDescription>
      </Field>

      <div className='flex flex-col gap-2'>
        <Text render={<p />} variant='body-2' weight='semibold'>
          Capabilities
        </Text>
        <label className='flex items-center gap-2 text-sm'>
          <Checkbox
            checked={state.isPublic}
            onCheckedChange={(checked) => set({ isPublic: checked })}
          />
          Public client — cannot keep a secret, authenticates with PKCE
        </label>
        <label className='flex items-center gap-2 text-sm'>
          <Checkbox
            checked={state.pkceEnabled}
            onCheckedChange={(checked) => set({ pkceEnabled: checked })}
          />
          Require PKCE at the code exchange
        </label>
        <label className='flex items-center gap-2 text-sm'>
          <Checkbox
            checked={state.requiresReauthentication}
            onCheckedChange={(checked) => set({ requiresReauthentication: checked })}
          />
          Require a fresh sign-in at every authorization
        </label>
        <label className='flex items-center gap-2 text-sm'>
          <Checkbox
            checked={state.requiresPar}
            onCheckedChange={(checked) => set({ requiresPar: checked })}
          />
          Require pushed authorization requests (PAR)
        </label>
        <label className='flex items-center gap-2 text-sm'>
          <Checkbox
            checked={state.skipConsent}
            onCheckedChange={(checked) => set({ skipConsent: checked })}
          />
          Skip the consent step after the first authorization
        </label>
      </div>

      <div className='flex flex-col gap-2'>
        <Text render={<p />} variant='body-2' weight='semibold'>
          Grant types
        </Text>
        {GRANT_TYPES.map((grant) => (
          <label key={grant.value} className='flex items-center gap-2 text-sm'>
            <Checkbox
              checked={state.grantTypes.includes(grant.value)}
              onCheckedChange={(checked) => toggleGrant(grant.value, checked)}
            />
            <span className='font-mono text-xs'>{grant.value}</span> — {grant.label}
          </label>
        ))}
        <Text render={<p />} variant='body-2' color='neutral-faded'>
          Leave every box unchecked to keep the registered default: authorization code, refresh
          token, and the device grant.
        </Text>
      </div>

      <div className='flex flex-col gap-2'>
        <Text render={<p />} variant='body-2' weight='semibold'>
          Back-channel logout
        </Text>
        <Field>
          <FieldLabel>Logout URI</FieldLabel>
          <Input
            value={state.backchannelLogoutUri}
            onChange={(event) => set({ backchannelLogoutUri: event.target.value })}
            placeholder='https://app.example.com/logout'
          />
          <FieldDescription>
            Where the provider POSTs the logout token when a session ends. Empty opts the client
            out.
          </FieldDescription>
        </Field>
        <label className='flex items-center gap-2 text-sm'>
          <Checkbox
            checked={state.backchannelSessionRequired}
            onCheckedChange={(checked) => set({ backchannelSessionRequired: checked })}
          />
          Require the session identity in the logout token
        </label>
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <Field>
          <FieldLabel>Access token lifetime (minutes)</FieldLabel>
          <Input
            type='number'
            min={1}
            value={state.accessTokenDuration}
            onChange={(event) => set({ accessTokenDuration: event.target.value })}
            placeholder='Default'
          />
        </Field>
        <Field>
          <FieldLabel>Refresh token lifetime (minutes)</FieldLabel>
          <Input
            type='number'
            min={1}
            value={state.refreshTokenDuration}
            onChange={(event) => set({ refreshTokenDuration: event.target.value })}
            placeholder='Default'
          />
        </Field>
      </div>
    </div>
  )
}
