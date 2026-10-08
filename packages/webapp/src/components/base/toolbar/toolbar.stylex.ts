import * as stylex from '@stylexjs/stylex'
import { colors } from 'components/styles/core/colors.stylex'
import { fontFamily } from 'components/styles/core/tokens.stylex'
import { stroke } from 'components/styles/core/tokens.stylex'
import { unit, radius } from 'components/styles/core/tokens.stylex'

export const toolbarStyles = stylex.create({
  root: {
    alignItems: 'center',
    borderColor: colors.borderNeutralFaded,
    borderRadius: radius.large,
    borderStyle: 'solid',
    borderWidth: stroke.ring1,
    display: 'flex',
    fontFamily: fontFamily.body,
    gap: unit.x1,
    padding: unit.x1,
    width: 'fit-content'
  },
  group: {
    alignItems: 'center',
    display: 'flex',
    gap: unit.x1
  },
  separator: {
    alignSelf: 'stretch',
    backgroundColor: colors.borderNeutralFaded,
    marginBlock: unit.x1,
    marginInline: unit.x1,
    width: stroke.ring1
  }
})
