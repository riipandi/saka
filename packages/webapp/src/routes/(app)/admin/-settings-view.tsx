import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { RotateCcw, SendHorizontal } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle
} from 'uilibs/components/base/alert-dialog'
import { Button } from 'uilibs/components/base/button'
import { Field, FieldDescription } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue
} from 'uilibs/components/base/select'
import { Switch } from 'uilibs/components/base/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from 'uilibs/components/base/tabs'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { Textarea } from 'uilibs/components/extra/textarea'
import { useAppConfig } from '#/hooks/use-app-config'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { pageStyles } from '#/styles/pages/page.stylex'
import { SettingsService } from '~/codegen/settings_pb'
import { SETTING_GROUPS, metaFor, type SettingMeta } from './-settings-catalog'
import { TestEmailDialog } from './-test-email-dialog'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value?: Timestamp | null): string {
  if (!value) return 'at the catalog default'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime())
    ? 'at the catalog default'
    : `since ${instantFormat.format(instant)}`
}

/**
 * The platform settings screen (Wave 5 phase 1): the deployment's
 * database-backed catalog, grouped the way auth-design names the
 * taxonomy, with the runtime configuration document beside it and the
 * mailer probe. Every write answers the wire verbatim — the catalog is
 * the contract, and a reset restores it.
 */
export function AdminSettingsView() {
  const settings = useQuery(SettingsService.method.list, {}, { retry: false })

  return (
    <div
      {...stylex.props(
        pageStyles.container,
        pageStyles.containerPadMedium,
        pageStyles.containerPadLarge,
        pageStyles.containerPadXLarge
      )}
    >
      <div {...stylex.props(pageStyles.header)}>
        <div {...stylex.props(pageStyles.headerLeft)}>
          <Text
            render={<p />}
            variant='caption-1'
            weight='semibold'
            color='primary'
            style={pageStyles.kicker}
          >
            Administration
          </Text>
          <Text render={<h1 />} variant='featured-4' weight='bold'>
            Platform settings
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The deployment's database-backed catalog — every change lands on the next read, no
            restart required.
          </Text>
        </div>
      </div>

      {settings.isPending ? (
        <Spinner />
      ) : settings.isError ? (
        <Text variant='body-2' color='critical'>
          {settings.error.message}
        </Text>
      ) : (
        <Tabs defaultValue={SETTING_GROUPS[0]?.id}>
          <TabsList>
            {SETTING_GROUPS.map((group) => (
              <TabsTrigger key={group.id} value={group.id}>
                {group.label}
              </TabsTrigger>
            ))}
            <TabsTrigger value='runtime'>Runtime</TabsTrigger>
          </TabsList>
          {SETTING_GROUPS.map((group) => (
            <TabsContent key={group.id} value={group.id}>
              <Text render={<p />} variant='body-2' color='neutral-faded'>
                {group.description}
              </Text>
              {group.sections.map((section) => (
                <SettingsSectionCard
                  key={section.title}
                  title={section.title}
                  keys={section.keys}
                  byKey={settings.data?.settings ?? []}
                />
              ))}
            </TabsContent>
          ))}
          <TabsContent value='runtime'>
            <RuntimeCard />
          </TabsContent>
        </Tabs>
      )}
    </div>
  )
}

function SettingsSectionCard({
  title,
  keys,
  byKey
}: {
  title: string
  keys: string[]
  byKey: SettingWire[]
}) {
  const rows = keys.flatMap((key) => {
    const found = byKey.find((setting) => setting.key === key)
    return found ? [found] : []
  })
  if (rows.length === 0) return null

  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent>
        <div {...stylex.props(styles.rows)}>
          {rows.map((row) => (
            <SettingRow key={row.key} setting={row} meta={metaFor(row.key)} />
          ))}
        </div>
      </CardContent>
    </Card>
  )
}

function SettingRow({ setting, meta }: { setting: SettingWire; meta: SettingMeta }) {
  const queryClient = useQueryClient()
  const listKey = createConnectQueryKey({
    schema: SettingsService.method.list,
    cardinality: 'finite'
  })
  const [resetting, setResetting] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)

  const update = useMutation(SettingsService.method.update, {
    onSuccess: () => {
      setFailed(null)
      void queryClient.invalidateQueries({ queryKey: listKey })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })
  const reset = useMutation(SettingsService.method.reset, {
    onSuccess: () => {
      setFailed(null)
      setResetting(false)
      void queryClient.invalidateQueries({ queryKey: listKey })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const dirty = setting.value !== setting.defaultValue
  const isOverridden = setting.updatedAt !== undefined

  return (
    <div {...stylex.props(styles.row)}>
      <div {...stylex.props(styles.rowText)}>
        <Text render={<p />} variant='body-2' weight='semibold'>
          {meta.label}
          {dirty && (
            <Badge variant='secondary' {...stylex.props(styles.badge)}>
              overridden
            </Badge>
          )}
          {setting.sealed && (
            <Badge variant='primary' {...stylex.props(styles.badge)}>
              sealed
            </Badge>
          )}
          {setting.public && (
            <Badge variant='outline' {...stylex.props(styles.badge)}>
              public
            </Badge>
          )}
        </Text>
        <Text render={<p />} variant='body-2' color='neutral-faded'>
          {meta.description}
        </Text>
        {failed !== null && (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        )}
      </div>
      <div {...stylex.props(styles.rowControl)}>
        <SettingControl
          setting={setting}
          meta={meta}
          pending={update.isPending}
          onWrite={(value) => update.mutate({ key: setting.key, value })}
        />
        <Button
          variant='ghost'
          size='sm'
          aria-label={`Reset ${meta.label} to default`}
          disabled={!isOverridden || reset.isPending}
          onClick={() => setResetting(true)}
        >
          <RotateCcw />
          Reset
        </Button>
      </div>
      <AlertDialog
        open={resetting}
        onOpenChange={(next) => {
          if (!next) setResetting(false)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Reset {meta.label}?</AlertDialogTitle>
            <AlertDialogDescription>
              The stored override is discarded and the catalog default takes over: "
              {setting.defaultValue || '(empty)'}". This lands on the next read.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                reset.mutate({ key: setting.key })
              }}
            >
              {reset.isPending ? <Spinner /> : 'Reset'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

/** The control a setting's kind renders: a switch commits on the flip, an
 * enum commits on the pick, and a numeric or free-text value stages in a
 * draft until the save answers. */
function SettingControl({
  setting,
  meta,
  pending,
  onWrite
}: {
  setting: SettingWire
  meta: SettingMeta
  pending: boolean
  onWrite: (value: string) => void
}) {
  const [draft, setDraft] = useState(setting.value)

  // The row re-renders when the listing invalidates; the scratch pad
  // follows the wire unless the operator is mid-edit.
  useEffect(() => {
    setDraft(setting.value)
  }, [setting.value])

  if (meta.kind === 'boolean') {
    const on = setting.value === 'true'
    return (
      <Switch
        checked={on}
        onCheckedChange={(checked) => onWrite(checked ? 'true' : 'false')}
        aria-label={meta.label}
      />
    )
  }

  if (meta.kind === 'enum' && meta.choices !== undefined) {
    const known = meta.choices.some((choice) => choice.value === setting.value)
    return (
      <Select
        value={setting.value}
        onValueChange={(next) => {
          if (typeof next === 'string') onWrite(next)
        }}
      >
        <SelectTrigger aria-label={meta.label}>
          <SelectValue>{known ? setting.value : `${setting.value} (unknown)`}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          {meta.choices.map((choice) => (
            <SelectItem key={choice.value} value={choice.value}>
              {choice.label}
            </SelectItem>
          ))}
          {!known && <SelectItem value={setting.value}>{setting.value}</SelectItem>}
        </SelectContent>
      </Select>
    )
  }

  const isNumber = meta.kind === 'number'
  return (
    <Field>
      <div {...stylex.props(styles.draftRow)}>
        {meta.multiline ? (
          <Textarea
            aria-label={meta.label}
            rows={3}
            value={draft}
            placeholder={meta.placeholder}
            onChange={(event) => setDraft(event.target.value)}
          />
        ) : (
          <Input
            type={isNumber ? 'number' : 'text'}
            aria-label={meta.label}
            value={draft}
            placeholder={meta.placeholder}
            min={meta.min}
            max={meta.max}
            onChange={(event) => setDraft(event.target.value)}
            {...stylex.props(isNumber ? styles.narrow : undefined)}
          />
        )}
        {isNumber && meta.unit !== undefined && <Badge variant='outline'>{meta.unit}</Badge>}
        <Button
          size='sm'
          disabled={draft === setting.value || pending}
          onClick={() => onWrite(draft)}
        >
          Save
        </Button>
      </div>
      <FieldDescription>{formatInstant(setting.updatedAt)}</FieldDescription>
    </Field>
  )
}

/** The runtime document the deployment serves: the public shape to anyone,
 * the full configuration to an administrator — the secrets redacted at
 * the source, never the client's job. */
function RuntimeCard() {
  const config = useAppConfig()
  const [testOpen, setTestOpen] = useState(false)

  return (
    <Card>
      <CardHeader>
        <CardTitle>Runtime configuration</CardTitle>
        <CardDescription>
          The document <code>GET /api/configuration</code> serves this caller — the immutable
          runtime facts, secrets redacted at the source.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {config.isPending ? (
          <Spinner />
        ) : config.isError ? (
          <Text variant='body-2' color='critical'>
            {getErrorMessage(config.error)}
          </Text>
        ) : (
          <>
            <pre {...stylex.props(styles.document)}>{JSON.stringify(config.data, null, 2)}</pre>
            <div {...stylex.props(styles.mailerRow)}>
              <div {...stylex.props(styles.rowText)}>
                <Text render={<p />} variant='body-2' weight='semibold'>
                  Mailer probe
                </Text>
                <Text render={<p />} variant='body-2' color='neutral-faded'>
                  Sends one rendered message through the deployment's mailer — acceptance says the
                  message was rendered and submitted, not that a mailbox took it.
                </Text>
              </div>
              <Button variant='outline' onClick={() => setTestOpen(true)}>
                <SendHorizontal />
                Send test email
              </Button>
            </div>
          </>
        )}
      </CardContent>
      <TestEmailDialog open={testOpen} onOpenChange={setTestOpen} />
    </Card>
  )
}

interface SettingWire {
  key: string
  value: string
  defaultValue: string
  sealed: boolean
  public: boolean
  updatedAt?: Timestamp | undefined
}

const styles = stylex.create({
  rows: {
    display: 'grid',
    gap: 20
  },
  row: {
    display: 'flex',
    alignItems: 'flex-start',
    justifyContent: 'space-between',
    gap: 24,
    borderBottom: '1px solid rgba(128,128,128,0.15)',
    paddingBottom: 16
  },
  rowText: {
    display: 'grid',
    gap: 4,
    maxWidth: 480
  },
  rowControl: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    flexShrink: 0
  },
  badge: {
    marginLeft: 6
  },
  draftRow: {
    display: 'flex',
    alignItems: 'center',
    gap: 8
  },
  narrow: {
    width: 120
  },
  document: {
    fontFamily: 'monospace',
    fontSize: 12,
    lineHeight: 1.5,
    whiteSpace: 'pre-wrap',
    wordBreak: 'break-word',
    background: 'rgba(128,128,128,0.08)',
    borderRadius: 8,
    padding: 16,
    maxHeight: 480,
    overflow: 'auto'
  },
  mailerRow: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 24,
    marginTop: 16
  }
})
