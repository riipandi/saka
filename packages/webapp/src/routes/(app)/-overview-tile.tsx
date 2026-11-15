import * as stylex from '@stylexjs/stylex'
import { Link } from '@tanstack/react-router'
import type { LinkProps } from '@tanstack/react-router'
import { Badge } from 'uilibs/components/extra/badge'
import { Card, CardContent } from 'uilibs/components/extra/card'
import {
  Item,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemTitle
} from 'uilibs/components/extra/item'
import { Skeleton } from 'uilibs/components/extra/skeleton'
import { Text } from 'uilibs/components/extra/text'
import { unit } from 'uilibs/styles/tokens.stylex'
import { TimeValue } from '#/components/time-value'
import { formatEvent } from '#/libraries/audit-event'
import type { AuditLog } from '~/codegen/auditlog_pb'

const styles = stylex.create({
  link: { color: 'inherit', display: 'block', textDecorationLine: 'none' },
  value: { alignItems: 'center', display: 'flex', gap: unit.x2, marginBottom: unit.x2 },
  skeleton: { height: '2rem', marginBottom: unit.x2, width: '4rem' },
  title: { marginBottom: unit.x2 }
})

/**
 * One count on the overview. A tile whose query failed, or whose answer carries
 * no total, renders nothing: an error is not a count of zero, and one slow or
 * refused list never holds the page.
 */
export function Tile({
  label,
  value,
  loading,
  failed,
  hint,
  to,
  warning = false
}: {
  label: string
  value: number | null | undefined
  loading: boolean
  failed: boolean
  hint?: string
  to?: LinkProps['to']
  warning?: boolean
}) {
  if (failed || (!loading && (value === null || value === undefined))) return null
  const card = (
    <Card>
      <CardContent>
        {loading ? (
          <Skeleton data-testid='tile-skeleton' {...stylex.props(styles.skeleton)} />
        ) : (
          <div {...stylex.props(styles.value)}>
            <Text render={<p />} variant='featured-4' weight='bold'>
              {value}
            </Text>
            {warning && value ? <Badge variant='destructive'>Needs attention</Badge> : null}
          </div>
        )}
        <Text render={<p />} variant='body-1' weight='semibold'>
          {label}
        </Text>
        {hint ? (
          <Text variant='body-2' color='neutral-faded'>
            {hint}
          </Text>
        ) : null}
      </CardContent>
    </Card>
  )
  return to ? (
    <Link to={to} {...stylex.props(styles.link)}>
      {card}
    </Link>
  ) : (
    card
  )
}

/** The five newest audit rows of whichever log the caller reads. Hidden on failure. */
export function ActivityCard({
  title,
  logs,
  loading,
  failed,
  emptyText
}: {
  title: string
  logs: AuditLog[]
  loading: boolean
  failed: boolean
  emptyText: string
}) {
  if (failed) return null
  return (
    <Card>
      <CardContent>
        <Text render={<p />} variant='body-1' weight='semibold' style={styles.title}>
          {title}
        </Text>
        {loading ? (
          <Skeleton data-testid='tile-skeleton' {...stylex.props(styles.skeleton)} />
        ) : logs.length === 0 ? (
          <Text variant='body-2' color='neutral-faded'>
            {emptyText}
          </Text>
        ) : (
          <ItemGroup>
            {logs.map((log) => (
              <Item key={log.id} size='sm'>
                <ItemContent>
                  <ItemTitle>{formatEvent(log.event)}</ItemTitle>
                  <ItemDescription>
                    {[log.username, log.ipAddress].filter(Boolean).join(' · ')}
                  </ItemDescription>
                </ItemContent>
                <TimeValue value={log.createdAt} />
              </Item>
            ))}
          </ItemGroup>
        )}
      </CardContent>
    </Card>
  )
}
