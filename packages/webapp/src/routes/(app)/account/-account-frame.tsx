import * as stylex from '@stylexjs/stylex'
import type { ReactNode } from 'react'
import { unit } from 'uilibs/styles/tokens.stylex'
import { PageHeader } from '#/components/-page-header'
import { pageStyles } from '#/styles/pages/page.stylex'

const styles = stylex.create({
  actions: {
    display: 'flex',
    justifyContent: 'flex-end',
    marginBottom: unit.x4
  }
})

/**
 * The page chrome the account screens shared before they became tabs.
 * Inside the settings shell the title already lives on the shell, so
 * `embedded` keeps the body and, when a screen had a header action, that
 * action.
 */
export function AccountFrame({
  embedded = false,
  title,
  description,
  actions,
  children
}: {
  embedded?: boolean
  title?: string
  description?: string
  actions?: ReactNode
  children: ReactNode
}) {
  if (embedded) {
    return (
      <>
        {actions ? <div {...stylex.props(styles.actions)}>{actions}</div> : null}
        {children}
      </>
    )
  }

  return (
    <div
      {...stylex.props(
        pageStyles.container,
        pageStyles.containerPadMedium,
        pageStyles.containerPadLarge,
        pageStyles.containerPadXLarge
      )}
    >
      <PageHeader
        kicker='Account'
        title={title ?? ''}
        description={description ?? ''}
        actions={actions}
      />
      {children}
    </div>
  )
}
