import * as stylex from '@stylexjs/stylex'
import { colors } from 'uilibs/styles/colors.stylex'
import { unit } from 'uilibs/styles/tokens.stylex'

// The loading shapes a page answers with while its query is in flight.
// Each composite mirrors the height of the content it stands in for, so the
// arrival of the data shifts nothing. @stylexjs/babel-plugin 0.19 only
// inlines `defineConsts` media keys declared in the SAME file — the values
// mirror `breakpoints` in `page.stylex.ts`; keep them in sync.
export const localBreakpoints = stylex.defineConsts({
  medium: '@media (min-width: 660px) and (max-width: 899px)',
  large: '@media (min-width: 900px) and (max-width: 1279px)',
  xlarge: '@media (min-width: 1280px)'
})

export const skeletonStyles = stylex.create({
  rowsStack: {
    display: 'flex',
    flexDirection: 'column',
    gap: unit.x2
  },
  rowLine: {
    height: 36
  },
  cardsGrid: {
    display: 'grid',
    gap: unit.x4
  },
  cardsGridMedium: {
    [localBreakpoints.medium]: { gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' }
  },
  cardsGridLarge: {
    [localBreakpoints.large]: { gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' }
  },
  cardsGridXLarge: {
    [localBreakpoints.xlarge]: { gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' }
  },
  cardBlock: {
    display: 'flex',
    flexDirection: 'column',
    gap: unit.x2,
    padding: unit.x4,
    borderRadius: 12,
    borderWidth: 1,
    borderStyle: 'solid',
    borderColor: colors.borderNeutralFaded
  },
  cardTitle: {
    height: 14,
    width: '55%'
  },
  cardLine: {
    height: 12,
    width: '90%'
  },
  cardLineShort: {
    height: 12,
    width: '40%'
  },
  formStack: {
    display: 'flex',
    flexDirection: 'column',
    gap: unit.x5
  },
  fieldGroup: {
    display: 'flex',
    flexDirection: 'column',
    gap: unit.x2
  },
  fieldLabel: {
    height: 12,
    width: 96
  },
  fieldInput: {
    height: 36,
    width: '100%'
  },
  formActions: {
    display: 'flex',
    flexDirection: 'row',
    justifyContent: 'flex-end',
    gap: unit.x2,
    marginTop: unit.x2
  },
  actionButton: {
    height: 36,
    width: 96
  }
})
