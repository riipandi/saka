import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from 'uilibs/components/extra/empty'
import { Spinner } from 'uilibs/components/extra/spinner'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from 'uilibs/components/extra/table'
import { Text } from 'uilibs/components/extra/text'
import { ListPagination } from '#/components/list-pagination'
import { listPageInput, usePaginatedList } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { dataTableStyles } from '#/styles/element/data-table.stylex'
import { pageStyles } from '#/styles/pages/page.stylex'
import { RunSchedulerJobNowRequestSchema, SchedulerService } from '~/codegen/system_pb'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value?: Timestamp | null): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/** The job's due state: overdue names itself — the claim cursor has moved
 * past the wall clock and the tick has not caught up yet. */
function dueState(nextDue?: Timestamp | null): {
  word: string
  color: 'neutral' | 'critical' | 'success'
} {
  if (!nextDue) return { word: 'unscheduled', color: 'neutral' }
  const due = timestampDate(nextDue)
  if (Number.isNaN(due.getTime())) return { word: 'unknown', color: 'neutral' }
  return due.getTime() <= Date.now()
    ? { word: 'due now', color: 'critical' }
    : { word: 'scheduled', color: 'success' }
}

/**
 * The ops console's scheduler half: the registered jobs, each with the
 * cron spec it fires on, the next-due cursor, and the last-fire instant.
 * RunNow enqueues one tick immediately without moving the schedule — the
 * next tick still fires on time — so the action needs no confirmation:
 * it spends nothing, it only starts the job's task early.
 */
export function SchedulerView({
  page,
  onPageChange
}: {
  page?: number
  onPageChange?: (page: number) => void
}) {
  const queryClient = useQueryClient()
  const [actionError, setActionError] = useState<string | null>(null)
  const [runId, setRunId] = useState<string | null>(null)

  const jobs = usePaginatedList(SchedulerService.method.listJobs, listPageInput(page))

  const runNow = useMutation(SchedulerService.method.runNow, {
    onSuccess: (_answer, input) => {
      setActionError(null)
      setRunId(null)
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: SchedulerService.method.listJobs,
          cardinality: 'finite'
        })
      })
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: SchedulerService.method.runNow,
          input,
          cardinality: 'finite'
        })
      })
    },
    onError: (error) => {
      setRunId(null)
      setActionError(getErrorMessage(error))
    }
  })

  const start = (id: string) => {
    setRunId(id)
    runNow.mutate(create(RunSchedulerJobNowRequestSchema, { id }))
  }

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
          <Text render={<p />} variant='body-2' weight='medium' color='primary'>
            Ops console
          </Text>
          <Text render={<h1 />} variant='featured-4' weight='bold'>
            Scheduler
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The registered jobs, the cron spec each fires on, and the claim cursor's answer for when
            it is next due. Run now starts a job's task early — the schedule itself does not move.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        {actionError ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {actionError}
          </Text>
        ) : null}

        <Card>
          <CardHeader>
            <CardTitle>Registered jobs</CardTitle>
            <CardDescription>
              In name order; the spec is the cron the tick fires on.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {jobs.isPending ? (
              <Spinner />
            ) : jobs.isError ? (
              <Text variant='body-2' color='critical'>
                {getErrorMessage(jobs.error)}
              </Text>
            ) : (jobs.data?.jobs ?? []).length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No jobs registered</EmptyTitle>
                  <EmptyDescription>
                    The process registers its jobs at boot; none answered this time.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Job</TableHead>
                    <TableHead>Cron spec</TableHead>
                    <TableHead>Next due</TableHead>
                    <TableHead>Last fired</TableHead>
                    <TableHead>State</TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(jobs.data?.jobs ?? []).map((job) => {
                    const state = dueState(job.nextDue)
                    return (
                      <TableRow key={job.id}>
                        <TableCell {...stylex.props(styles.mono)}>{job.name}</TableCell>
                        <TableCell {...stylex.props(styles.mono)}>{job.spec}</TableCell>
                        <TableCell {...stylex.props(dataTableStyles.instant)}>
                          {formatInstant(job.nextDue)}
                        </TableCell>
                        <TableCell {...stylex.props(dataTableStyles.instant)}>
                          {formatInstant(job.lastFired)}
                        </TableCell>
                        <TableCell>
                          <Badge variant={state.color === 'critical' ? 'destructive' : 'secondary'}>
                            {state.word}
                          </Badge>
                        </TableCell>
                        <TableCell>
                          <Button
                            variant='outline'
                            size='xs'
                            disabled={runNow.isPending}
                            onClick={() => start(job.id)}
                          >
                            {runId === job.id ? <Spinner /> : null}
                            Run now
                          </Button>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            )}
          </CardContent>
          {jobs.summary && (jobs.summary.hasPrevious || jobs.summary.hasNext) ? (
            <CardFooter>
              <ListPagination
                page={jobs.summary.page}
                totalPages={jobs.summary.totalPages}
                hasPrevious={jobs.summary.hasPrevious}
                hasNext={jobs.summary.hasNext}
                onPageChange={(next) => onPageChange?.(next)}
              />
            </CardFooter>
          ) : null}
        </Card>
      </div>
    </div>
  )
}

const styles = stylex.create({
  mono: {
    fontFamily: 'var(--font-monospace, monospace)',
    fontSize: 13
  }
})
