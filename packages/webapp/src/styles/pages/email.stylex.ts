import * as stylex from '@stylexjs/stylex'
import { unit } from 'uilibs/styles/tokens.stylex'

/**
 * Email page-only layout bits; everything else comes from shared components
 * (Card, Field, Input, OTPField, Badge, Text, Button).
 */
export const styles = stylex.create({
  addressRow: {
    alignItems: 'center',
    display: 'flex',
    flexWrap: 'wrap',
    gap: unit.x5
  },
  codeRow: {
    alignItems: 'center',
    display: 'flex',
    flexWrap: 'wrap',
    gap: unit.x5
  },
  grow: {
    display: 'flex',
    flexDirection: 'column',
    flexGrow: 1,
    gap: unit.x2,
    minWidth: '12rem'
  },
  note: {
    marginTop: unit.x3
  }
})
