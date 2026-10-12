import * as stylex from '@stylexjs/stylex'
import { unit } from 'uilibs/styles/tokens.stylex'

/**
 * The body block a form sheet scrolls: the SheetContent is a flex column
 * with a padded header and footer, so the fields between them carry their
 * own horizontal padding and own the overflow — a twelve-field form scrolls
 * under a fixed title and a footer that never leaves.
 */
export const sheetFormStyles = stylex.create({
  body: {
    display: 'flex',
    flex: 1,
    flexDirection: 'column',
    gap: unit.x4,
    minHeight: 0,
    overflowY: 'auto',
    paddingLeft: unit.x4,
    paddingRight: unit.x4
  }
})
