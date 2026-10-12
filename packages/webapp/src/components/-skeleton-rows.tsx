import * as stylex from '@stylexjs/stylex'
import { Skeleton } from 'uilibs/components/extra/skeleton'
import { skeletonStyles as s } from '#/styles/element/skeletons.stylex'

/**
 * The loading shape of a table page: one pulse block per row, sized to the
 * row height the real table lands at. A bare spinner centers itself and
 * lets the table's arrival shove the footer; these rows hold the space.
 */
export function TableSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div aria-hidden data-testid='table-skeleton' {...stylex.props(s.rowsStack)}>
      {Array.from({ length: rows }, (_, row) => (
        <Skeleton key={row} {...stylex.props(s.rowLine)} />
      ))}
    </div>
  )
}
