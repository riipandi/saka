import * as stylex from '@stylexjs/stylex'
import { Skeleton } from 'uilibs/components/extra/skeleton'
import { skeletonStyles as s } from '#/styles/element/skeletons.stylex'

/**
 * The loading shape of a form or detail pane: label-then-input pairs, and
 * when `actions` is set a right-aligned button block that keeps the
 * footer's submit from jumping. Detail panes render the same pairs — a
 * definition list is a label block and a value block to a reader's eye.
 */
export function FormSkeleton({
  fields = 3,
  actions = true
}: {
  fields?: number
  actions?: boolean
}) {
  return (
    <div aria-hidden data-testid='form-skeleton' {...stylex.props(s.formStack)}>
      {Array.from({ length: fields }, (_, field) => (
        <div key={field} {...stylex.props(s.fieldGroup)}>
          <Skeleton {...stylex.props(s.fieldLabel)} />
          <Skeleton {...stylex.props(s.fieldInput)} />
        </div>
      ))}
      {actions && (
        <div {...stylex.props(s.formActions)}>
          <Skeleton {...stylex.props(s.actionButton)} />
        </div>
      )}
    </div>
  )
}
