import { create } from '@bufbuild/protobuf'
import { useMutation } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useEffect, useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Checkbox } from 'uilibs/components/base/checkbox'
import { Field, FieldDescription, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue
} from 'uilibs/components/base/select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle
} from 'uilibs/components/extra/sheet'
import { Text } from 'uilibs/components/extra/text'
import { Textarea } from 'uilibs/components/extra/textarea'
import { sheetFormStyles } from '#/styles/element/sheet-form.stylex'
import {
  CreateOAuthConnectionRequestSchema,
  OAuthAttributeMappingSchema,
  OAuthCustomAttributeSchema,
  OAuthEndpointsSchema,
  OAuthSSOService,
  UpdateOAuthConnectionRequestSchema
} from '~/codegen/authn_pb'
import type { OAuthConnection } from '~/codegen/authn_pb'

/** The builtin kinds the code ships, with the defaults their rows start from. */
const BUILTIN_PRESETS: Record<string, { displayName: string; scopes: string }> = {
  google: { displayName: 'Google', scopes: 'openid email profile' },
  github: { displayName: 'GitHub', scopes: 'read:user user:email' }
}

/**
 * The provider form (phase 3): create and edit over one sheet. A builtin
 * kind borrows the code's endpoints; a custom kind carries a discovery
 * document or a manual set, and may rewrite which claims answer the
 * account's fields. The secret is write-only — an edit that leaves it
 * empty keeps the stored one.
 */
export function OAuthProviderSheet({
  open,
  onOpenChange,
  connection,
  onDone,
  takenProviders = []
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  connection: OAuthConnection | null
  onDone: () => void
  takenProviders?: string[]
}) {
  const editing = connection !== null

  const [kind, setKind] = useState<'builtin' | 'custom'>('builtin')
  const [provider, setProvider] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [discoveryMode, setDiscoveryMode] = useState<'discovery' | 'manual'>('discovery')
  const [discoveryUrl, setDiscoveryUrl] = useState('')
  const [authorizationUrl, setAuthorizationUrl] = useState('')
  const [tokenUrl, setTokenUrl] = useState('')
  const [userinfoUrl, setUserinfoUrl] = useState('')
  const [jwksUrl, setJwksUrl] = useState('')
  const [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const [scopes, setScopes] = useState('')
  const [enabled, setEnabled] = useState(false)
  const [mappingJson, setMappingJson] = useState('')
  const [customAttributesJson, setCustomAttributesJson] = useState('')
  const [failed, setFailed] = useState<string | null>(null)

  // The form is a scratch pad: opening it seeds from the connection as it
  // stands, so a create starts empty and an edit starts from the row.
  useEffect(() => {
    if (open) {
      setFailed(null)
      setKind(connection?.kind === 'custom' ? 'custom' : 'builtin')
      setProvider(connection?.provider ?? '')
      setDisplayName(connection?.displayName ?? '')
      const manual =
        connection !== null && connection.kind === 'custom' && connection.discoveryUrl === ''
      setDiscoveryMode(connection === null || manual ? 'discovery' : 'manual')
      setDiscoveryUrl(connection?.discoveryUrl ?? '')
      setAuthorizationUrl(connection?.endpoints?.authorization ?? '')
      setTokenUrl(connection?.endpoints?.token ?? '')
      setUserinfoUrl(connection?.endpoints?.userinfo ?? '')
      setJwksUrl(connection?.endpoints?.jwks ?? '')
      setClientId(connection?.clientId ?? '')
      setClientSecret('')
      setScopes(connection?.scopes ?? '')
      setEnabled(connection?.enabled ?? false)
      setMappingJson(
        connection !== null && connection.kind === 'custom'
          ? mappingToJson(connection.attributeMapping)
          : ''
      )
      setCustomAttributesJson(
        connection !== null && connection.kind === 'custom'
          ? JSON.stringify(
              connection.customAttributes.map((attribute) => ({
                key: attribute.key,
                claim: attribute.claim
              })),
              null,
              2
            )
          : ''
      )
    }
  }, [open, connection])

  const createConnection = useMutation(OAuthSSOService.method.createConnection, {
    onSuccess: () => {
      onOpenChange(false)
      onDone()
    },
    onError: (error) => setFailed(error.message)
  })
  const updateConnection = useMutation(OAuthSSOService.method.updateConnection, {
    onSuccess: () => {
      onOpenChange(false)
      onDone()
    },
    onError: (error) => setFailed(error.message)
  })

  const submit = () => {
    setFailed(null)
    let mapping: Record<string, unknown> | null = null
    if (kind === 'custom' && mappingJson.trim() !== '') {
      try {
        const parsed: unknown = JSON.parse(mappingJson)
        if (!isRecord(parsed)) {
          setFailed('the attribute mapping is not a JSON object')
          return
        }
        mapping = parsed
      } catch {
        setFailed('the attribute mapping is not valid JSON')
        return
      }
    }
    let attributes: Array<{ key: string; claim: string }> | null = null
    if (kind === 'custom' && customAttributesJson.trim() !== '') {
      try {
        const parsed: unknown = JSON.parse(customAttributesJson)
        if (!isCustomAttributes(parsed)) {
          setFailed('the custom attributes are not a JSON list of { key, claim } entries')
          return
        }
        attributes = parsed
      } catch {
        setFailed('the custom attributes are not valid JSON')
        return
      }
    }

    if (!editing) {
      const preset = BUILTIN_PRESETS[provider]
      createConnection.mutate(
        create(CreateOAuthConnectionRequestSchema, {
          kind,
          provider,
          displayName: displayName.trim() === '' ? (preset?.displayName ?? '') : displayName,
          discoveryUrl: kind === 'custom' && discoveryMode === 'discovery' ? discoveryUrl : '',
          endpoints:
            kind === 'custom' && discoveryMode === 'manual'
              ? create(OAuthEndpointsSchema, {
                  authorization: authorizationUrl,
                  token: tokenUrl,
                  userinfo: userinfoUrl,
                  jwks: jwksUrl
                })
              : undefined,
          clientId,
          clientSecret,
          scopes,
          attributeMapping:
            kind === 'custom' && mapping !== null ? mappingFromJson(mapping) : undefined,
          customAttributes:
            kind === 'custom' && attributes !== null
              ? attributes.map((attribute) => create(OAuthCustomAttributeSchema, attribute))
              : undefined,
          enabled
        })
      )
      return
    }

    const isCustom = connection.kind === 'custom'
    updateConnection.mutate(
      create(UpdateOAuthConnectionRequestSchema, {
        id: connection.id,
        displayName,
        discoveryUrl: isCustom && discoveryMode === 'discovery' ? discoveryUrl : undefined,
        endpoints:
          isCustom && discoveryMode === 'manual'
            ? create(OAuthEndpointsSchema, {
                authorization: authorizationUrl,
                token: tokenUrl,
                userinfo: userinfoUrl,
                jwks: jwksUrl
              })
            : undefined,
        clientId,
        clientSecret: clientSecret === '' ? undefined : clientSecret,
        scopes,
        attributeMapping: isCustom && mapping !== null ? mappingFromJson(mapping) : undefined,
        customAttributes:
          isCustom && attributes !== null
            ? attributes.map((attribute) => create(OAuthCustomAttributeSchema, attribute))
            : isCustom
              ? []
              : undefined,
        enabled
      })
    )
  }

  const pending = createConnection.isPending || updateConnection.isPending
  const builtinPreset = BUILTIN_PRESETS[provider]

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side='right'>
        <SheetHeader>
          <SheetTitle>{editing ? `Edit ${connection.displayName}` : 'Add a provider'}</SheetTitle>
          <SheetDescription>
            {editing
              ? 'The secrets are never read back — a new secret replaces the stored one only when set.'
              : 'One connection per provider slug; the secret is stored sealed and never shown again.'}
          </SheetDescription>
        </SheetHeader>

        <div {...stylex.props(sheetFormStyles.body)}>
          {!editing && (
            <Field>
              <FieldLabel>Kind</FieldLabel>
              <Select
                value={kind}
                onValueChange={(next) => {
                  setKind(next ?? 'builtin')
                  setProvider('')
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value='builtin'>
                    Builtin — Google or GitHub, endpoints shipped by the code
                  </SelectItem>
                  <SelectItem value='custom'>
                    Custom — any OIDC provider by discovery or manual endpoints
                  </SelectItem>
                </SelectContent>
              </Select>
            </Field>
          )}

          {kind === 'builtin' ? (
            <Field>
              <FieldLabel>Provider</FieldLabel>
              {editing ? (
                <Text variant='body-2'>{provider}</Text>
              ) : (
                <Select value={provider} onValueChange={(next) => setProvider(next ?? '')}>
                  <SelectTrigger>
                    <SelectValue placeholder='pick the provider' />
                  </SelectTrigger>
                  <SelectContent>
                    {Object.entries(BUILTIN_PRESETS).map(([slug, preset]) => {
                      // The builtin slugs are singletons — the backend's
                      // unique provider index binds one connection per
                      // slug, so a taken one is named here rather than
                      // discovered at submit.
                      const taken = takenProviders.includes(slug)
                      return (
                        <SelectItem key={slug} value={slug} disabled={taken}>
                          {preset.displayName}
                          {taken ? ' — already added' : ''}
                        </SelectItem>
                      )
                    })}
                  </SelectContent>
                </Select>
              )}
              {!editing && builtinPreset !== undefined && (
                <FieldDescription>
                  Defaults: display name {builtinPreset.displayName}, scopes {builtinPreset.scopes}.
                </FieldDescription>
              )}
            </Field>
          ) : (
            <Field>
              <FieldLabel>Provider slug</FieldLabel>
              {editing ? (
                <Text variant='body-2'>{provider}</Text>
              ) : (
                <Input value={provider} onChange={(event) => setProvider(event.target.value)} />
              )}
              <FieldDescription>
                The word the sign-in start route names — one connection per slug.
              </FieldDescription>
            </Field>
          )}

          <Field>
            <FieldLabel>Display name</FieldLabel>
            <Input value={displayName} onChange={(event) => setDisplayName(event.target.value)} />
            <FieldDescription>The name the sign-in page renders on the button.</FieldDescription>
          </Field>

          {kind === 'custom' && (
            <>
              <Field>
                <FieldLabel>Endpoints</FieldLabel>
                <Select
                  value={discoveryMode}
                  onValueChange={(next) => setDiscoveryMode(next ?? 'discovery')}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value='discovery'>Discovery document</SelectItem>
                    <SelectItem value='manual'>Manual endpoints</SelectItem>
                  </SelectContent>
                </Select>
                <FieldDescription>
                  Naming either source clears the other when the connection is saved.
                </FieldDescription>
              </Field>
              {discoveryMode === 'discovery' ? (
                <Field>
                  <FieldLabel>Discovery URL</FieldLabel>
                  <Input
                    value={discoveryUrl}
                    onChange={(event) => setDiscoveryUrl(event.target.value)}
                    placeholder='https://provider.example.com/.well-known/openid-configuration'
                  />
                </Field>
              ) : (
                <div {...stylex.props(styles.grid)}>
                  <Field>
                    <FieldLabel>Authorization</FieldLabel>
                    <Input
                      value={authorizationUrl}
                      onChange={(event) => setAuthorizationUrl(event.target.value)}
                    />
                  </Field>
                  <Field>
                    <FieldLabel>Token</FieldLabel>
                    <Input value={tokenUrl} onChange={(event) => setTokenUrl(event.target.value)} />
                  </Field>
                  <Field>
                    <FieldLabel>Userinfo</FieldLabel>
                    <Input
                      value={userinfoUrl}
                      onChange={(event) => setUserinfoUrl(event.target.value)}
                    />
                    <FieldDescription>
                      Leave empty when the id_token carries the claims.
                    </FieldDescription>
                  </Field>
                  <Field>
                    <FieldLabel>JWKS</FieldLabel>
                    <Input value={jwksUrl} onChange={(event) => setJwksUrl(event.target.value)} />
                  </Field>
                </div>
              )}
            </>
          )}

          <Field>
            <FieldLabel>Client ID</FieldLabel>
            <Input value={clientId} onChange={(event) => setClientId(event.target.value)} />
          </Field>

          <Field>
            <FieldLabel>{editing ? 'New client secret' : 'Client secret'}</FieldLabel>
            <Input
              type='password'
              value={clientSecret}
              onChange={(event) => setClientSecret(event.target.value)}
            />
            {editing && <FieldDescription>Leave empty to keep the stored secret.</FieldDescription>}
          </Field>

          <Field>
            <FieldLabel>Scopes</FieldLabel>
            <Input
              value={scopes}
              onChange={(event) => setScopes(event.target.value)}
              placeholder={
                builtinPreset?.scopes ?? 'space-separated scopes; empty keeps the default set'
              }
            />
          </Field>

          {kind === 'custom' && (
            <>
              <Field>
                <FieldLabel>Attribute mapping (JSON)</FieldLabel>
                <Textarea
                  aria-label='Attribute mapping'
                  rows={6}
                  value={mappingJson}
                  onChange={(event) => setMappingJson(event.target.value)}
                  placeholder={'{\n  "email": "mail",\n  "subject": "uid"\n}'}
                />
                <FieldDescription>
                  Which provider claim answers each account field — email, given_name, family_name,
                  subject, email_verified, email_verified_default, username, picture. Unset keys
                  keep the default claim names; empty keeps the stored mapping.
                </FieldDescription>
              </Field>
              <Field>
                <FieldLabel>Custom attributes (JSON)</FieldLabel>
                <Textarea
                  aria-label='Custom attributes'
                  rows={6}
                  value={customAttributesJson}
                  onChange={(event) => setCustomAttributesJson(event.target.value)}
                  placeholder={'[\n  { "key": "tenant", "claim": "org_id" }\n]'}
                />
                <FieldDescription>
                  Operator-defined attributes read off the claims and stored on the account; an
                  attribute whose claim the provider does not answer lands nothing.
                </FieldDescription>
              </Field>
            </>
          )}

          <Field>
            <FieldLabel>Enabled</FieldLabel>
            <label {...stylex.props(styles.choice)}>
              <Checkbox checked={enabled} onCheckedChange={(checked) => setEnabled(checked)} />
              Accept sign-ins through this provider
            </label>
            <FieldDescription>
              A disabled provider keeps its linked accounts but answers no new sign-ins.
            </FieldDescription>
          </Field>

          {failed !== null && (
            <Text variant='body-2' color='critical'>
              {failed}
            </Text>
          )}
        </div>

        <SheetFooter>
          <Button variant='ghost' onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={pending}>
            {pending ? 'Saving…' : editing ? 'Save changes' : 'Add provider'}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

/** The JSON the textarea edits turned into the wire's mapping shape. */
const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value)

const isCustomAttributes = (value: unknown): value is Array<{ key: string; claim: string }> =>
  Array.isArray(value) &&
  value.every(
    (entry) => isRecord(entry) && typeof entry.key === 'string' && typeof entry.claim === 'string'
  )

/** The JSON the textarea edits turned into the wire's mapping shape. */
function mappingFromJson(
  mapping: Record<string, unknown>
): ReturnType<typeof create<typeof OAuthAttributeMappingSchema>> {
  return create(OAuthAttributeMappingSchema, {
    email: typeof mapping.email === 'string' ? mapping.email : '',
    givenName: typeof mapping.given_name === 'string' ? mapping.given_name : '',
    familyName: typeof mapping.family_name === 'string' ? mapping.family_name : '',
    subject: typeof mapping.subject === 'string' ? mapping.subject : '',
    emailVerified: typeof mapping.email_verified === 'string' ? mapping.email_verified : '',
    emailVerifiedDefault:
      typeof mapping.email_verified_default === 'boolean'
        ? mapping.email_verified_default
        : undefined,
    username: typeof mapping.username === 'string' ? mapping.username : '',
    picture: typeof mapping.picture === 'string' ? mapping.picture : ''
  })
}

/** The stored mapping rendered back as the JSON the textarea edits. */
function mappingToJson(mapping: OAuthConnection['attributeMapping']): string {
  if (mapping === undefined || mapping === null) return ''
  const entries: Record<string, unknown> = {}
  if (mapping.email !== '') entries.email = mapping.email
  if (mapping.givenName !== '') entries.given_name = mapping.givenName
  if (mapping.familyName !== '') entries.family_name = mapping.familyName
  if (mapping.subject !== '') entries.subject = mapping.subject
  if (mapping.emailVerified !== '') entries.email_verified = mapping.emailVerified
  if (mapping.emailVerifiedDefault !== undefined)
    entries.email_verified_default = mapping.emailVerifiedDefault
  if (mapping.username !== '') entries.username = mapping.username
  if (mapping.picture !== '') entries.picture = mapping.picture
  return Object.keys(entries).length === 0 ? '' : JSON.stringify(entries, null, 2)
}

const styles = stylex.create({
  grid: {
    display: 'grid',
    gap: 12
  },
  choice: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    fontSize: 14
  }
})
