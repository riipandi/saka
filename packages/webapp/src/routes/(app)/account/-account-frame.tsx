import * as stylex from '@stylexjs/stylex'
import type { ReactNode } from 'react'
import { Text } from 'uilibs/components/extra/text'
import { unit } from 'uilibs/styles/tokens.stylex'
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
      <div {...stylex.props(pageStyles.header)}>
        <div {...stylex.props(pageStyles.headerLeft)}>
          <Text
            render={<p />}
            variant='caption-1'
            weight='semibold'
            color='primary'
            style={pageStyles.kicker}
          >
            Account
          </Text>
          <Text render={<h1 />} variant='featured-4' weight='bold'>
            {title}
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            {description}
          </Text>
        </div>
        {actions}
      </div>
      {children}
    </div>
  )
}
