import * as stylex from '@stylexjs/stylex'
import { colors } from 'uilibs/styles/colors.stylex'
import { unit } from 'uilibs/styles/tokens.stylex'

export const styles = stylex.create({
  listStack: {
    display: 'flex',
    flexDirection: 'column',
    gap: unit.x2
  },
  factorRow: {
    display: 'flex',
    alignItems: 'center',
    gap: unit.x3,
    padding: unit.x3,
    borderRadius: unit.x2,
    border: `1px solid ${colors.borderNeutral}`
  },
  factorIcon: {
    width: 20,
    height: 20,
    flexShrink: 0
  },
  factorMain: {
    display: 'flex',
    flexDirection: 'column',
    gap: unit.x1,
    flex: 1
  },
  toolbarRow: {
    display: 'flex',
    gap: unit.x3
  },
  codes: {
    display: 'grid',
    gridTemplateColumns: 'repeat(2, 1fr)',
    gap: unit.x2,
    fontFamily: 'monospace'
  }
})
