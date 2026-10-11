import * as stylex from '@stylexjs/stylex'
import { Text } from 'uilibs/components/extra/text'
import { pageStyles } from '#/styles/pages/page.stylex'

/**
 * The placeholder the admin screens render until their wave phase lands —
 * the route exists (the sidebar's link must resolve) and the page says so
 * without pretending to work.
 */
export function PlaceholderView({ page }: { page: string }) {
  return (
    <div
      {...stylex.props(
        pageStyles.container,
        pageStyles.containerPadMedium,
        pageStyles.containerPadLarge
      )}
    >
      <Text render={<h1 />} variant='featured-4' weight='bold'>
        {page}
      </Text>
      <Text variant='body-2' color='neutral-faded'>
        This screen is on its way in the admin console wave.
      </Text>
    </div>
  )
}
