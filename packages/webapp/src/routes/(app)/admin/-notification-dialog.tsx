import { create } from '@bufbuild/protobuf'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Checkbox } from 'uilibs/components/base/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from 'uilibs/components/base/dialog'
import { Field, FieldDescription, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue
} from 'uilibs/components/base/select'
import { Switch } from 'uilibs/components/base/switch'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { Textarea } from 'uilibs/components/extra/textarea'
import { LIST_MAX_LIMIT, listPageInput } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import {
  ListUserGroupsRequestSchema,
  ListUsersRequestSchema,
  UserGroupService,
  UserService
} from '~/codegen/identity_pb'
import { NotificationService } from '~/codegen/notification_pb'

type AudienceKind = 'global' | 'users' | 'user_groups'
type Category = 'announcement' | 'system'

/**
 * The publish dialog (Wave 5 phase 2): one announcement or system notice
 * to its audience. The audience is a kind, not a fanout — global costs
 * one row however many accounts hold; the named kinds resolve at read
 * time. A `system` notice names accounts and carries no topic.
 */
export function NotificationBroadcastDialog({
  open,
  onOpenChange
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [category, setCategory] = useState<Category>('announcement')
  const [topic, setTopic] = useState('')
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [audienceKind, setAudienceKind] = useState<AudienceKind>('global')
  const [userIds, setUserIds] = useState<string[]>([])
  const [groupIds, setGroupIds] = useState<string[]>([])
  const [sendEmail, setSendEmail] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)

  const queryClient = useQueryClient()

  const users = useQuery(
    UserService.method.listUsers,
    create(ListUsersRequestSchema, listPageInput(1, LIST_MAX_LIMIT)),
    { enabled: open && audienceKind === 'users', retry: false }
  )
  const groups = useQuery(
    UserGroupService.method.listUserGroups,
    create(ListUserGroupsRequestSchema, listPageInput(1, LIST_MAX_LIMIT)),
    { enabled: open && audienceKind === 'user_groups', retry: false }
  )

  const publish = useMutation(NotificationService.method.createNotification, {
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: NotificationService.method.listAllNotifications,
          cardinality: 'finite'
        })
      })
      close(false)
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const close = (next: boolean) => {
    if (!next) {
      setCategory('announcement')
      setTopic('')
      setTitle('')
      setBody('')
      setAudienceKind('global')
      setUserIds([])
      setGroupIds([])
      setSendEmail(false)
      setFailed(null)
    }
    onOpenChange(next)
  }

  const canSubmit =
    title.trim() !== '' &&
    body.trim() !== '' &&
    (category !== 'system' || userIds.length > 0) &&
    (audienceKind !== 'users' || userIds.length > 0) &&
    (audienceKind !== 'user_groups' || groupIds.length > 0)

  const submit = () => {
    setFailed(null)
    publish.mutate({
      category,
      topic: category === 'announcement' && topic.trim() !== '' ? topic.trim() : undefined,
      title: title.trim(),
      body: body.trim(),
      audienceKind,
      userIds: audienceKind === 'users' ? userIds : [],
      userGroupIds: audienceKind === 'user_groups' ? groupIds : [],
      sendEmail: sendEmail || undefined
    })
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New broadcast</DialogTitle>
          <DialogDescription>
            An announcement targets everyone unless it names accounts or groups; a system notice is
            about specific accounts and carries no topic.
          </DialogDescription>
        </DialogHeader>

        <Field>
          <FieldLabel>Category</FieldLabel>
          <Select
            value={category}
            onValueChange={(next) => {
              if (next === 'system' || next === 'announcement') {
                setCategory(next)
                if (next === 'system') {
                  setTopic('')
                  // A system notice must name its audience; preselect the
                  // accounts kind so the pair stays expressible.
                  if (audienceKind === 'global') setAudienceKind('users')
                }
              }
            }}
          >
            <SelectTrigger aria-label='Category'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='announcement'>Announcement</SelectItem>
              <SelectItem value='system'>System notice</SelectItem>
            </SelectContent>
          </Select>
        </Field>

        {category === 'announcement' && (
          <Field>
            <FieldLabel>Topic</FieldLabel>
            <Input
              value={topic}
              maxLength={64}
              placeholder='optional subject, e.g. maintenance'
              onChange={(event) => setTopic(event.target.value)}
            />
          </Field>
        )}

        <Field>
          <FieldLabel htmlFor='broadcast-title'>Title</FieldLabel>
          <Input
            id='broadcast-title'
            value={title}
            maxLength={200}
            onChange={(event) => setTitle(event.target.value)}
          />
        </Field>

        <Field>
          <FieldLabel htmlFor='broadcast-body'>Body</FieldLabel>
          <Textarea
            id='broadcast-body'
            value={body}
            maxLength={2000}
            rows={4}
            onChange={(event) => setBody(event.target.value)}
          />
        </Field>

        <Field>
          <FieldLabel>Audience</FieldLabel>
          <Select
            value={audienceKind}
            onValueChange={(next) => {
              if (next === 'global' || next === 'users' || next === 'user_groups') {
                setAudienceKind(next)
                if (next === 'user_groups') setUserIds([])
                if (next === 'users') setGroupIds([])
              }
            }}
          >
            <SelectTrigger aria-label='Audience'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='global'>Everyone (global)</SelectItem>
              <SelectItem value='users'>Named accounts</SelectItem>
              <SelectItem value='user_groups'>Groups</SelectItem>
            </SelectContent>
          </Select>
          {audienceKind === 'users' && (
            <AudiencePicker
              ariaLabel='Accounts in audience'
              options={(users.data?.users ?? []).map((user) => ({
                id: user.id,
                label: user.displayName !== '' ? `${user.displayName} (${user.email})` : user.email
              }))}
              loading={users.isPending}
              selected={userIds}
              onToggle={(id) =>
                setUserIds((current) =>
                  current.includes(id) ? current.filter((one) => one !== id) : [...current, id]
                )
              }
              empty='No account matched — the audience stays empty until one is named.'
            />
          )}
          {audienceKind === 'user_groups' && (
            <AudiencePicker
              ariaLabel='Groups in audience'
              options={(groups.data?.groups ?? []).map((group) => ({
                id: group.id,
                label: group.displayName !== '' ? group.displayName : group.name
              }))}
              loading={groups.isPending}
              selected={groupIds}
              onToggle={(id) =>
                setGroupIds((current) =>
                  current.includes(id) ? current.filter((one) => one !== id) : [...current, id]
                )
              }
              empty='No group matched — the audience stays empty until one is named.'
            />
          )}
          <FieldDescription>
            The audience resolves at read time — an account joining a group later sees what the
            group was named for.
          </FieldDescription>
        </Field>

        <Field>
          <div {...stylex.props(styles.switchRow)}>
            <Switch
              id='notification-send-email'
              checked={sendEmail}
              onCheckedChange={(checked) => setSendEmail(checked)}
              aria-label='Also send as email'
            />
            <FieldLabel htmlFor='notification-send-email'>Also send as email</FieldLabel>
          </div>
          <FieldDescription>
            Rides the deployment's announcement-email switch — a mailer the deployment switched off
            makes this a no-op.
          </FieldDescription>
        </Field>

        {failed !== null && (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        )}

        <DialogFooter>
          <Button variant='ghost' onClick={() => close(false)}>
            Close
          </Button>
          <Button disabled={!canSubmit || publish.isPending} onClick={submit}>
            {publish.isPending ? <Spinner /> : 'Publish'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/**
 * One audience picker: a scrolled checkbox list over the named kind's
 * candidates, shared by the accounts and the groups pass.
 */
function AudiencePicker({
  ariaLabel,
  options,
  loading,
  selected,
  onToggle,
  empty
}: {
  ariaLabel: string
  options: Array<{ id: string; label: string }>
  loading: boolean
  selected: string[]
  onToggle: (id: string) => void
  empty: string
}) {
  if (loading) return <Spinner />
  if (options.length === 0) {
    return <FieldDescription>{empty}</FieldDescription>
  }
  return (
    <div role='group' aria-label={ariaLabel} {...stylex.props(styles.audienceList)}>
      {options.map((option) => {
        const checked = selected.includes(option.id)
        return (
          <label key={option.id} {...stylex.props(styles.audienceRow)}>
            <Checkbox
              checked={checked}
              onCheckedChange={() => onToggle(option.id)}
              aria-label={option.label}
            />
            <span>{option.label}</span>
          </label>
        )
      })}
    </div>
  )
}

const styles = stylex.create({
  switchRow: {
    display: 'flex',
    alignItems: 'center',
    gap: 10
  },
  audienceList: {
    display: 'flex',
    flexDirection: 'column',
    gap: 6,
    maxHeight: 180,
    overflowY: 'auto',
    marginTop: 8
  },
  audienceRow: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    cursor: 'pointer'
  }
})
