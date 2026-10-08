import * as stylex from '@stylexjs/stylex'
import { colors } from 'components/styles/core/colors.stylex'
import { stroke } from 'components/styles/core/tokens.stylex'

export const separatorStyles = stylex.create({
  root: {
    backgroundColor: colors.borderNeutralFaded,
    flexShrink: 0
  }
})

export const separatorOrientations = stylex.create({
  horizontal: {
    height: stroke.ring1,
    width: '100%'
  },
  vertical: {
    alignSelf: 'stretch',
    width: stroke.ring1
  }
})
