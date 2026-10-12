import * as stylex from '@stylexjs/stylex'
import { Text } from 'uilibs/components/extra/text'
import { pageStyles } from '#/styles/pages/page.stylex'

/**
 * The opening block every authenticated page shares: the section kicker,
 * the title, the one-line description, and the page-level actions on the
 * right. The views that rolled this markup by hand move to this component
 * so a header can never drift out of the arrangement.
 */
export function PageHeader({
  kicker,
  title,
  description,
  actions
}: {
  kicker: string
  title: string
  description: string
  actions?: React.ReactNode
}) {
  return (
    <div {...stylex.props(pageStyles.header)}>
      <div {...stylex.props(pageStyles.headerLeft)}>
        <Text
          render={<p />}
          variant='caption-1'
          weight='semibold'
          color='primary'
          style={pageStyles.kicker}
        >
          {kicker}
        </Text>
        <Text render={<h1 />} variant='featured-4' weight='bold'>
          {title}
        </Text>
        <Text variant='body-2' color='neutral-faded'>
          {description}
        </Text>
      </div>
      {actions != null && <div {...stylex.props(localStyles.actions)}>{actions}</div>}
    </div>
  )
}

const localStyles = stylex.create({
  actions: {
    alignItems: 'center',
    display: 'flex',
    flexShrink: 0,
    gap: 8
  }
})
