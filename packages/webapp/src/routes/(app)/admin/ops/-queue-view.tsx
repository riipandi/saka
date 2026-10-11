import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger
} from 'uilibs/components/base/alert-dialog'
import { Button } from 'uilibs/components/base/button'
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerHeader,
  DrawerTitle
} from 'uilibs/components/base/drawer'
import { Tabs, TabsList, TabsTrigger } from 'uilibs/components/base/tabs'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardAction,
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
import type {
  ListDeadTasksResponse,
  ListQueuesResponse,
  ListTasksResponse
} from '~/codegen/system_pb'
import {
  CancelTaskRequestSchema,
  FlushCompletedTasksRequestSchema,
  FlushPendingTasksRequestSchema,
  GetTaskRequestSchema,
  QueueService,
  ReplayDeadTasksRequestSchema
} from '~/codegen/system_pb'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value?: Timestamp | null): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

function formatDuration(ms?: number | null): string {
  if (ms == null) return '—'
  if (ms < 1000) return `${Math.round(ms)} ms`
  return `${(ms / 1000).toFixed(2)} s`
}

/** The stored payload's read: the bytes the queue kept decode to the
 * task's own JSON, pretty-printed for the drawer. A body that will not
 * parse renders raw — a corrupt row is information, not a crash. */
function formatPayload(payload: Uint8Array | undefined): string {
  if (!payload || payload.length === 0) return ''
  const text = new TextDecoder().decode(payload)
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

/** The tab the URL names; anything else is the pending table. */
type QueueTab = 'pending' | 'dead'

/** The typed list result the cards render — the hook's answer with the
 * response shape restored, so the rows carry their wire types instead of
 * the descriptor's bare `Message<string>`. */
type QueueList<O> = Omit<ReturnType<typeof usePaginatedList>, 'data' | 'summary'> & {
  data?: O | undefined
  summary: ReturnType<typeof usePaginatedList>['summary']
}

export interface QueueSearch {
  page?: number
  deadPage?: number
  tab?: QueueTab
}

/**
 * The ops console's queue half: the registered queues with their live
 * counts, the pending table, the dead archive, and the actions the wire
 * admits — cancel one unclaimed task, replay a queue's dead pile, flush
 * a table. Every destructive action confirms first; the replay is not,
 * it only re-enqueues what the archive already keeps.
 */
export function QueueView({
  page,
  deadPage,
  tab,
  onSearchChange
}: {
  page?: number
  deadPage?: number
  tab?: QueueTab
  onSearchChange?: (next: Partial<QueueSearch>) => void
}) {
  const queryClient = useQueryClient()
  const activeTab: QueueTab = tab ?? 'pending'
  const livePage = page ?? 1
  const archivePage = deadPage ?? 1

  const [taskDrawer, setTaskDrawer] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

  const queues = usePaginatedList(QueueService.method.listQueues, listPageInput(livePage, 100))
  const pending = usePaginatedList(QueueService.method.listTasks, listPageInput(livePage))
  const dead = usePaginatedList(QueueService.method.listDeadTasks, listPageInput(archivePage))

  // Every write moves rows across the three surfaces, so each invalidates
  // them all — the counts answer the movement, not the last read.
  const afterWrite = () => {
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: QueueService.method.listQueues,
        input: listPageInput(livePage, 100),
        cardinality: 'finite'
      })
    })
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: QueueService.method.listTasks,
        cardinality: 'finite'
      })
    })
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: QueueService.method.listDeadTasks,
        cardinality: 'finite'
      })
    })
  }

  const withError = (run: () => void) => {
    setActionError(null)
    try {
      run()
    } catch (error) {
      setActionError(getErrorMessage(error))
    }
  }

  const replay = useMutation(QueueService.method.replayDeadTasks, {
    onSuccess: () => {
      setActionError(null)
      afterWrite()
    },
    onError: (error) => setActionError(getErrorMessage(error))
  })

  const flushPending = useMutation(QueueService.method.flushPendingTasks, {
    onSuccess: () => {
      setActionError(null)
      afterWrite()
    },
    onError: (error) => setActionError(getErrorMessage(error))
  })

  const flushCompleted = useMutation(QueueService.method.flushCompletedTasks, {
    onSuccess: () => {
      setActionError(null)
      afterWrite()
    },
    onError: (error) => setActionError(getErrorMessage(error))
  })

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
            Task queues
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The queues the process registered, the tasks waiting on workers, and the failures the
            archive keeps for replay.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        {actionError ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {actionError}
          </Text>
        ) : null}

        <QueuesCard queues={queues} />

        <Tabs
          value={activeTab}
          onValueChange={(next) => {
            if (next === 'pending' || next === 'dead') onSearchChange?.({ tab: next })
          }}
        >
          <TabsList>
            <TabsTrigger value='pending'>Pending</TabsTrigger>
            <TabsTrigger value='dead'>Dead archive</TabsTrigger>
          </TabsList>
        </Tabs>

        {activeTab === 'pending' ? (
          <PendingCard
            pending={pending}
            working={flushPending.isPending}
            onInspect={setTaskDrawer}
            onPageChange={(next) => onSearchChange?.({ page: next })}
            onFlush={() =>
              withError(() => flushPending.mutate(create(FlushPendingTasksRequestSchema)))
            }
          />
        ) : (
          <DeadCard
            dead={dead}
            replaying={replay.isPending}
            onInspect={setTaskDrawer}
            onReplay={() => withError(() => replay.mutate(create(ReplayDeadTasksRequestSchema)))}
            onFlush={() =>
              withError(() => flushCompleted.mutate(create(FlushCompletedTasksRequestSchema)))
            }
            onPageChange={(next) => onSearchChange?.({ deadPage: next })}
          />
        )}
      </div>

      <TaskDrawer taskId={taskDrawer} onClose={() => setTaskDrawer(null)} />
    </div>
  )
}

/** The registered queues: one row each with its attempt budget, timeout,
 * retention, and the live counts the actions answer. */
function QueuesCard({ queues }: { queues: QueueList<ListQueuesResponse> }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Registered queues</CardTitle>
        <CardDescription>The configuration the process registered each queue with.</CardDescription>
      </CardHeader>
      <CardContent>
        {queues.isPending ? (
          <Spinner />
        ) : queues.isError ? (
          <Text variant='body-2' color='critical'>
            {getErrorMessage(queues.error)}
          </Text>
        ) : (queues.data?.queues ?? []).length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No queues registered</EmptyTitle>
              <EmptyDescription>
                The process registers its queues at boot; none answered this time.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Queue</TableHead>
                <TableHead>Max attempts</TableHead>
                <TableHead>Timeout</TableHead>
                <TableHead>Retention</TableHead>
                <TableHead>Pending</TableHead>
                <TableHead>Dead</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(queues.data?.queues ?? []).map((queue) => (
                <TableRow key={queue.name}>
                  <TableCell {...stylex.props(styles.mono)}>{queue.name}</TableCell>
                  <TableCell>{queue.maxAttempts}</TableCell>
                  <TableCell>{queue.timeoutMs > 0 ? `${queue.timeoutMs} ms` : '—'}</TableCell>
                  <TableCell>
                    {queue.retentionSeconds
                      ? `${queue.retentionSeconds}s${queue.retentionOnlyFailed ? ' (failed only)' : ''}`
                      : '—'}
                  </TableCell>
                  <TableCell>{queue.pending}</TableCell>
                  <TableCell>{queue.dead}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}

/** The pending table: the tasks waiting for a worker, running, or past
 * their wait, newest first. Cancel reaches only the unclaimed — the wire
 * refuses a claimed task with the precondition's own words. */
function PendingCard({
  pending,
  working,
  onInspect,
  onPageChange,
  onFlush
}: {
  pending: QueueList<ListTasksResponse>
  working: boolean
  onInspect: (id: string) => void
  onPageChange: (page: number) => void
  onFlush: () => void
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Pending tasks</CardTitle>
        <CardDescription>Waiting, running, or past their wait — newest first.</CardDescription>
        <CardAction>
          <FlushDialog
            label='Flush pending'
            title='Flush every unclaimed task?'
            description='Every unclaimed task is removed. Claimed tasks are in flight — they finish their lifecycle instead of vanishing under a worker.'
            pending={working}
            onConfirm={onFlush}
          />
        </CardAction>
      </CardHeader>
      <CardContent>
        {pending.isPending ? (
          <Spinner />
        ) : pending.isError ? (
          <Text variant='body-2' color='critical'>
            {getErrorMessage(pending.error)}
          </Text>
        ) : (pending.data?.tasks ?? []).length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No pending tasks</EmptyTitle>
              <EmptyDescription>
                Every enqueued task has been claimed and finished.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Task</TableHead>
                <TableHead>Queue</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Attempts</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>Wait until</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {(pending.data?.tasks ?? []).map((task) => (
                <TableRow key={task.id}>
                  <TableCell>
                    <button
                      type='button'
                      {...stylex.props(styles.mono, styles.taskLink)}
                      onClick={() => onInspect(task.id)}
                    >
                      {task.id}
                    </button>
                  </TableCell>
                  <TableCell {...stylex.props(styles.mono)}>{task.queue}</TableCell>
                  <TableCell>
                    <Badge variant={task.state === 'running' ? 'secondary' : 'outline'}>
                      {task.state}
                    </Badge>
                  </TableCell>
                  <TableCell>{task.attempts}</TableCell>
                  <TableCell {...stylex.props(dataTableStyles.instant)}>
                    {formatInstant(task.createdAt)}
                  </TableCell>
                  <TableCell {...stylex.props(dataTableStyles.instant)}>
                    {formatInstant(task.waitUntil)}
                  </TableCell>
                  <TableCell>
                    {task.state === 'pending' ? <CancelDialog taskId={task.id} /> : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
      {pending.summary && (pending.summary.hasPrevious || pending.summary.hasNext) ? (
        <CardFooter>
          <ListPagination
            page={pending.summary.page}
            totalPages={pending.summary.totalPages}
            hasPrevious={pending.summary.hasPrevious}
            hasNext={pending.summary.hasNext}
            onPageChange={onPageChange}
          />
        </CardFooter>
      ) : null}
    </Card>
  )
}

/** The dead archive: the failures that exhausted their attempts, kept for
 * the replay until their queue's retention expires them. */
function DeadCard({
  dead,
  replaying,
  onInspect,
  onReplay,
  onFlush,
  onPageChange
}: {
  dead: QueueList<ListDeadTasksResponse>
  replaying: boolean
  onInspect: (id: string) => void
  onReplay: () => void
  onFlush: () => void
  onPageChange: (page: number) => void
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Dead archive</CardTitle>
        <CardDescription>
          The tasks that exhausted their attempts, kept with their payload for the replay.
        </CardDescription>
        <CardAction>
          <div {...stylex.props(styles.toolbar)}>
            <Button variant='outline' size='sm' disabled={replaying} onClick={onReplay}>
              {replaying ? <Spinner /> : null}
              Replay all
            </Button>
            <FlushDialog
              label='Flush completed'
              title='Flush the completed archive?'
              description='Every completed record is removed, retention notwithstanding. The dead pile stays — it is the raw material the replay reads.'
              onConfirm={onFlush}
            />
          </div>
        </CardAction>
      </CardHeader>
      <CardContent>
        {dead.isPending ? (
          <Spinner />
        ) : dead.isError ? (
          <Text variant='body-2' color='critical'>
            {getErrorMessage(dead.error)}
          </Text>
        ) : (dead.data?.tasks ?? []).length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No dead tasks</EmptyTitle>
              <EmptyDescription>
                No task has exhausted its attempts — or the archive has already expired them.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Record</TableHead>
                <TableHead>Queue</TableHead>
                <TableHead>Attempts</TableHead>
                <TableHead>Last error</TableHead>
                <TableHead>Failed at</TableHead>
                <TableHead>Expires</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(dead.data?.tasks ?? []).map((task) => (
                <TableRow key={task.id}>
                  <TableCell>
                    <button
                      type='button'
                      {...stylex.props(styles.mono, styles.taskLink)}
                      onClick={() => onInspect(task.id)}
                    >
                      {task.id}
                    </button>
                  </TableCell>
                  <TableCell {...stylex.props(styles.mono)}>{task.queue}</TableCell>
                  <TableCell>{task.attempts}</TableCell>
                  <TableCell {...stylex.props(styles.errorCell)}>{task.error || '—'}</TableCell>
                  <TableCell {...stylex.props(dataTableStyles.instant)}>
                    {formatInstant(task.lastExecutedAt)}
                  </TableCell>
                  <TableCell {...stylex.props(dataTableStyles.instant)}>
                    {formatInstant(task.expiresAt)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
      {dead.summary && (dead.summary.hasPrevious || dead.summary.hasNext) ? (
        <CardFooter>
          <ListPagination
            page={dead.summary.page}
            totalPages={dead.summary.totalPages}
            hasPrevious={dead.summary.hasPrevious}
            hasNext={dead.summary.hasNext}
            onPageChange={onPageChange}
          />
        </CardFooter>
      ) : null}
    </Card>
  )
}

/** Cancel one unclaimed task. The wire refuses a claimed task — the
 * precondition's message names itself in the drawer's error line. */
function CancelDialog({ taskId }: { taskId: string }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)

  const cancelling = useMutation(QueueService.method.cancelTask, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: QueueService.method.listTasks,
          cardinality: 'finite'
        })
      })
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: QueueService.method.listQueues,
          cardinality: 'finite'
        })
      })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger render={<Button variant='ghost' size='xs' />}>Cancel</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Cancel task {taskId}?</AlertDialogTitle>
          <AlertDialogDescription>
            The task is removed from the pending table. A claimed task cannot be cancelled — the
            call would answer the precondition, not the removal.
          </AlertDialogDescription>
          {failed ? (
            <Text render={<p />} variant='body-2' color='critical'>
              {failed}
            </Text>
          ) : null}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Keep</AlertDialogCancel>
          <AlertDialogAction
            variant='destructive'
            disabled={cancelling.isPending}
            onClick={() => cancelling.mutate(create(CancelTaskRequestSchema, { id: taskId }))}
          >
            {cancelling.isPending ? <Spinner /> : null}
            Cancel task
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** The destructive flush pair: the pending flush empties the waiting
 * table, the completed flush empties the archive's successes. Both count
 * what went; the count is the confirmation's answer. */
function FlushDialog({
  label,
  title,
  description,
  pending,
  onConfirm
}: {
  label: string
  title: string
  description: string
  pending?: boolean
  onConfirm: () => void
}) {
  return (
    <AlertDialog>
      <AlertDialogTrigger render={<Button variant='outline' size='sm' disabled={pending} />}>
        {pending ? <Spinner /> : null}
        {label}
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Keep</AlertDialogCancel>
          <AlertDialogAction variant='destructive' onClick={onConfirm}>
            Flush
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** The task drawer: one task's full view — state, attempts, timing, and
 * the decoded payload. A task neither table names answers not_found; the
 * drawer renders that refusal as its own empty state. */
function TaskDrawer({ taskId, onClose }: { taskId: string | null; onClose: () => void }) {
  const task = useQuery(
    QueueService.method.getTask,
    create(GetTaskRequestSchema, { id: taskId ?? '' }),
    { enabled: taskId !== null, retry: false }
  )

  const payload = formatPayload(task.data?.payload)

  return (
    <Drawer open={taskId !== null} onOpenChange={(open) => !open && onClose()}>
      <DrawerContent>
        <DrawerHeader>
          <DrawerTitle>Task {taskId}</DrawerTitle>
          <DrawerDescription>
            The task's state and the payload as the queue stored it.
          </DrawerDescription>
        </DrawerHeader>
        <div {...stylex.props(styles.drawerBody)}>
          {task.isPending ? (
            <Spinner />
          ) : task.isError ? (
            <Text variant='body-2' color='critical'>
              {getErrorMessage(task.error)}
            </Text>
          ) : task.data ? (
            <div {...stylex.props(pageStyles.stack)}>
              <div {...stylex.props(styles.factGrid)}>
                <Fact label='Queue' value={task.data.queue || '—'} mono />
                <Fact
                  label='State'
                  value={
                    <Badge
                      variant={
                        task.data.state === 'failure'
                          ? 'destructive'
                          : task.data.state === 'success'
                            ? 'secondary'
                            : 'outline'
                      }
                    >
                      {task.data.state}
                    </Badge>
                  }
                />
                <Fact label='Attempts' value={String(task.data.attempts ?? '—')} />
                <Fact label='Last duration' value={formatDuration(task.data.lastDurationMs)} />
                <Fact label='Created' value={formatInstant(task.data.createdAt)} mono />
                <Fact label='Last executed' value={formatInstant(task.data.lastExecutedAt)} mono />
              </div>
              {task.data.error ? (
                <div>
                  <Text render={<p />} variant='body-1' weight='semibold'>
                    Last error
                  </Text>
                  <pre {...stylex.props(styles.payload, styles.errorPre)}>{task.data.error}</pre>
                </div>
              ) : null}
              {payload ? (
                <div>
                  <Text render={<p />} variant='body-1' weight='semibold'>
                    Payload
                  </Text>
                  <pre {...stylex.props(styles.payload)}>{payload}</pre>
                </div>
              ) : (
                <Text variant='body-2' color='neutral-faded'>
                  The archive no longer holds this task's payload.
                </Text>
              )}
            </div>
          ) : null}
        </div>
      </DrawerContent>
    </Drawer>
  )
}

function Fact({
  label,
  value,
  mono = false
}: {
  label: string
  value: React.ReactNode
  mono?: boolean
}) {
  return (
    <div {...stylex.props(styles.fact)}>
      <Text variant='body-2' color='neutral-faded'>
        {label}
      </Text>
      <Text render={<p />} variant='body-1' style={mono ? styles.mono : undefined}>
        {value}
      </Text>
    </div>
  )
}

const styles = stylex.create({
  mono: {
    fontFamily: 'var(--font-monospace, monospace)',
    fontSize: 13
  },
  taskLink: {
    cursor: 'pointer',
    textDecoration: 'underline',
    textDecorationStyle: 'dotted',
    background: 'none',
    border: 'none',
    padding: 0,
    textAlign: 'left',
    color: 'inherit'
  },
  toolbar: {
    display: 'flex',
    gap: 8
  },
  errorCell: {
    maxWidth: 320,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap'
  },
  drawerBody: {
    overflowY: 'auto',
    padding: 16
  },
  factGrid: {
    display: 'grid',
    gap: 12,
    gridTemplateColumns: 'repeat(2, minmax(0, 1fr))'
  },
  fact: {
    display: 'flex',
    flexDirection: 'column',
    gap: 2
  },
  payload: {
    fontFamily: 'monospace',
    fontSize: 12,
    lineHeight: 1.5,
    whiteSpace: 'pre-wrap',
    wordBreak: 'break-word',
    border: '1px solid var(--color-border, #e5e5e5)',
    borderRadius: 8,
    padding: 12,
    maxHeight: 320,
    overflowY: 'auto',
    margin: 0
  },
  errorPre: {
    color: 'var(--color-critical, #b91c1c)'
  }
})
