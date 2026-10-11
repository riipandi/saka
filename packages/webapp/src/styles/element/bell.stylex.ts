import * as stylex from '@stylexjs/stylex'
import { colors } from 'uilibs/styles/colors.stylex'
import { fontSize, fontWeight, unit } from 'uilibs/styles/tokens.stylex'

/**
 * The notification bell's two states: the icon button the header floats,
 * and the count badge that pins to its corner. The red is the one accent
 * a count badge may claim — every other surface keeps the palette's own
 * colors.
 */
export const styles = stylex.create({
  bell: {
    position: 'relative',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 36,
    height: 36,
    padding: 0,
    borderWidth: 0,
    borderRadius: 999,
    backgroundColor: 'transparent',
    color: 'inherit',
    cursor: 'pointer',
    ':hover': {
      backgroundColor: 'rgba(128, 128, 128, 0.12)'
    }
  },
  flyout: {
    display: 'flex',
    flexDirection: 'column',
    width: '20rem',
    padding: 0
  },
  flyoutHeader: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: unit.x2,
    paddingTop: unit.x3,
    paddingBottom: unit.x2,
    paddingLeft: unit.x3,
    paddingRight: unit.x3
  },
  flyoutTitle: {
    fontSize: fontSize.body2,
    fontWeight: fontWeight.semibold,
    color: colors.foregroundNeutral
  },
  textButton: {
    borderWidth: 0,
    padding: 0,
    backgroundColor: 'transparent',
    color: colors.foregroundPrimary,
    fontSize: fontSize.caption1,
    fontWeight: fontWeight.medium,
    cursor: 'pointer',
    fontFamily: 'inherit'
  },
  flyoutStatus: {
    margin: 0,
    paddingTop: unit.x2,
    paddingBottom: unit.x4,
    paddingLeft: unit.x3,
    paddingRight: unit.x3,
    fontSize: fontSize.body2,
    color: colors.foregroundNeutralFaded
  },
  list: {
    listStyle: 'none',
    margin: 0,
    padding: 0,
    maxHeight: '18rem',
    overflowY: 'auto'
  },
  row: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'flex-start',
    gap: 2,
    width: '100%',
    paddingTop: unit.x2,
    paddingBottom: unit.x2,
    paddingLeft: unit.x3,
    paddingRight: unit.x3,
    borderWidth: 0,
    backgroundColor: 'transparent',
    textAlign: 'left',
    cursor: 'pointer',
    fontFamily: 'inherit',
    ':hover': {
      backgroundColor: colors.backgroundNeutralFaded
    }
  },
  rowTitle: {
    fontSize: fontSize.body2,
    fontWeight: fontWeight.medium,
    color: colors.foregroundNeutral,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    maxWidth: '100%'
  },
  rowBody: {
    fontSize: fontSize.caption1,
    color: colors.foregroundNeutralFaded,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    maxWidth: '100%'
  },
  rowTime: {
    fontSize: fontSize.caption1,
    color: colors.foregroundNeutralFaded
  },
  viewAll: {
    display: 'block',
    paddingTop: unit.x2,
    paddingBottom: unit.x3,
    paddingLeft: unit.x3,
    paddingRight: unit.x3,
    borderTopWidth: 1,
    borderTopStyle: 'solid',
    borderTopColor: colors.borderNeutralFaded,
    fontSize: fontSize.body2,
    fontWeight: fontWeight.medium,
    color: colors.foregroundPrimary,
    textDecoration: 'none'
  },
  badge: {
    position: 'absolute',
    top: -2,
    right: -4,
    minWidth: 16,
    height: 16,
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    paddingInline: 4,
    borderRadius: 999,
    backgroundColor: '#dc2626',
    color: '#ffffff',
    fontSize: 10,
    fontWeight: 700,
    lineHeight: 1
  }
})
