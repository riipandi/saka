import * as stylex from '@stylexjs/stylex'
import { unit } from 'uilibs/styles/tokens.stylex'

/**
 * The standalone verify-email screen's shell: one centered card on a bare
 * page, sized to the twelve-slot code entry.
 */
export const styles = stylex.create({
  page: {
    alignItems: 'center',
    display: 'flex',
    justifyContent: 'center',
    minHeight: '100vh',
    padding: unit.x5
  },
  card: {
    width: '100%',
    maxWidth: '40rem'
  },
  content: {
    alignItems: 'center',
    display: 'flex',
    flexDirection: 'column',
    gap: unit.x4
  },
  icon: {
    color: 'var(--color-success, #16a34a)'
  }
})
