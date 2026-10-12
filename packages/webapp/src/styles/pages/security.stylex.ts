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
  enrollRow: {
    marginTop: unit.x4,
    justifyContent: 'flex-start'
  },
  codes: {
    display: 'grid',
    gridTemplateColumns: 'repeat(2, 1fr)',
    gap: unit.x2,
    fontFamily: 'monospace'
  },
  enrollPanel: {
    display: 'flex',
    flexDirection: 'column',
    gap: unit.x3,
    marginTop: unit.x3,
    padding: unit.x4,
    borderRadius: unit.x2,
    border: `1px solid ${colors.borderNeutral}`
  },
  enrollQr: {
    display: 'flex',
    justifyContent: 'center',
    padding: unit.x2,
    background: '#fff',
    borderRadius: unit.x2,
    width: 192
  },
  enrollSecret: {
    fontFamily: 'monospace',
    wordBreak: 'break-all'
  },
  enrollFooter: {
    display: 'flex',
    gap: unit.x3,
    justifyContent: 'flex-start'
  }
})
