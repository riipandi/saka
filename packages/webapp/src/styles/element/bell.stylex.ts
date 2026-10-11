import * as stylex from '@stylexjs/stylex'

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
    borderRadius: 999,
    color: 'inherit',
    textDecoration: 'none',
    ':hover': {
      backgroundColor: 'rgba(128, 128, 128, 0.12)'
    }
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
