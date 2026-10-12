import { create } from '@bufbuild/protobuf'
import { useQuery } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle
} from 'uilibs/components/base/dialog'
import { Field, FieldLabel } from 'uilibs/components/base/field'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue
} from 'uilibs/components/base/select'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { colors } from 'uilibs/styles/colors.stylex'
import { fontFamily } from 'uilibs/styles/tokens.stylex'
import { ListError } from '#/components/-list-error'
import { LIST_MAX_LIMIT, listPageInput } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { OidcClientService, PreviewOidcClientRequestSchema } from '~/codegen/federation_pb'
import { ListUsersRequestSchema, UserService } from '~/codegen/identity_pb'

const previewStyles = stylex.create({
  stack: {
    display: 'flex',
    flexDirection: 'column',
    gap: 12
  },
  block: {
    display: 'flex',
    flexDirection: 'column',
    gap: 4
  },
  pre: {
    maxHeight: '16rem',
    overflow: 'auto',
    backgroundColor: colors.backgroundNeutralFaded,
    borderRadius: 8,
    padding: 12,
    fontSize: 12,
    fontFamily: fontFamily.monospace,
    whiteSpace: 'pre-wrap',
    wordBreak: 'break-all'
  }
})

/**
 * The claims a client would receive for one account — the preview builds the
 * maps from the account as it stands and mints no token. The account is the
 * operator's pick; nothing here is a secret.
 */
export function PreviewClientDialog({
  clientId,
  open,
  onOpenChange
}: {
  clientId: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [userId, setUserId] = useState('')

  const accounts = useQuery(
    UserService.method.listUsers,
    create(ListUsersRequestSchema, listPageInput(1, LIST_MAX_LIMIT)),
    { enabled: open }
  )
  const users = accounts.data?.users ?? []

  const preview = useQuery(
    OidcClientService.method.previewClient,
    create(PreviewOidcClientRequestSchema, { id: clientId, userId }),
    { enabled: open && userId !== '' }
  )

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Claim preview</DialogTitle>
          <DialogDescription>
            What this client would receive for one account — built as it stands, no token minted.
          </DialogDescription>
        </DialogHeader>
        <Field>
          <FieldLabel>Account</FieldLabel>
          <Select value={userId} onValueChange={(next) => setUserId(next ?? '')}>
            <SelectTrigger>
              <SelectValue placeholder='Choose the account to preview' />
            </SelectTrigger>
            <SelectContent>
              {users.map((user) => (
                <SelectItem key={user.id} value={user.id}>
                  {user.displayName || user.username}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        {userId === '' ? (
          <Text variant='body-2' color='neutral-faded'>
            Pick an account — the preview answers with its claim maps.
          </Text>
        ) : preview.isPending ? (
          <Spinner />
        ) : preview.isError ? (
          <ListError
            message={getErrorMessage(preview.error)}
            onRetry={() => void preview.refetch()}
          />
        ) : (
          <div {...stylex.props(previewStyles.stack)}>
            <PreviewBlock title='ID token' struct={preview.data?.preview?.idToken} />
            <PreviewBlock title='Access token' struct={preview.data?.preview?.accessToken} />
            <PreviewBlock title='Userinfo' struct={preview.data?.preview?.userInfo} />
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

/** The claim map arrives as plain JSON — the wire's Struct is JSON's own
 * shape, and the preview prints it as the token would have carried it. */
function PreviewBlock({ title, struct }: { title: string; struct?: unknown }) {
  return (
    <div {...stylex.props(previewStyles.block)}>
      <Text render={<p />} variant='body-2' weight='semibold'>
        {title}
      </Text>
      <pre {...stylex.props(previewStyles.pre)}>
        {struct ? JSON.stringify(struct, null, 2) : '{}'}
      </pre>
    </div>
  )
}
