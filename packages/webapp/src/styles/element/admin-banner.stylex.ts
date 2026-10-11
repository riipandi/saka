import * as stylex from '@stylexjs/stylex'
import { colors } from 'uilibs/styles/colors.stylex'
import { unit } from 'uilibs/styles/tokens.stylex'

/**
 * The impersonation banner — a full-width warning bar above the app content.
 * Its job is to say who is acting and to offer the way out; everything else
 * comes from shared components (Button, Text).
 */
export const styles = stylex.create({
  banner: {
    alignItems: 'center',
    backgroundColor: colors.backgroundWarningFaded,
    borderBottom: `1px solid ${colors.borderWarningFaded}`,
    display: 'flex',
    flexWrap: 'wrap',
    gap: unit.x3,
    paddingBlock: unit.x2,
    paddingInline: unit.x5
  },
  label: {
    color: colors.foregroundWarning,
    display: 'flex',
    alignItems: 'center',
    gap: unit.x2
  },
  grow: {
    flex: 1,
    minWidth: 0
  }
})
