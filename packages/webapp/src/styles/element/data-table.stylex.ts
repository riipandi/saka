import * as stylex from '@stylexjs/stylex'
import { fontFamily } from 'uilibs/styles/tokens.stylex'

/** Instants in a data table: a mono face keeps columns of times aligned. */
export const dataTableStyles = stylex.create({
  instant: {
    fontFamily: fontFamily.monospace,
    fontVariantNumeric: 'tabular-nums'
  }
})
