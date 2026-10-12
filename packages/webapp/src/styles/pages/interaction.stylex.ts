import * as stylex from '@stylexjs/stylex'
import { unit } from 'uilibs/styles/tokens.stylex'

/**
 * The interaction screen's shell: one centered card on a bare page — the
 * browser the provider redirected here has no app chrome, only the
 * question the relying party's flow paused on.
 */
export const styles = stylex.create({
  page: {
    alignItems: 'center',
    display: 'flex',
    justifyContent: 'center',
    minHeight: '100vh',
    padding: unit.x5
  },
  card: {
    width: '100%',
    maxWidth: '26rem'
  },
  content: {
    alignItems: 'stretch',
    display: 'flex',
    flexDirection: 'column',
    gap: unit.x4
  },
  actions: {
    display: 'flex',
    flexDirection: 'row-reverse',
    gap: unit.x2
  },
  scopes: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: unit.x2
  }
})
