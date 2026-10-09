import * as stylex from '@stylexjs/stylex'
import { unit } from 'uilibs/styles/tokens.stylex'

/** The audit page's layout bits; the rest comes from shared components. */
export const styles = stylex.create({
  icon: {
    height: 16,
    width: 16
  },
  toolbarActions: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: unit.x2
  },
  sort: {
    minWidth: '10rem'
  },
  pager: {
    alignItems: 'center',
    display: 'flex',
    gap: unit.x3,
    justifyContent: 'space-between'
  },
  pagerStatus: {
    flexShrink: 0
  }
})
