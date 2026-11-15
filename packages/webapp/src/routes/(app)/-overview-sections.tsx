import * as stylex from '@stylexjs/stylex'
import { Text } from 'uilibs/components/extra/text'
import { unit } from 'uilibs/styles/tokens.stylex'
import { AccountOverview } from './-overview-account'
import { AdminOverview } from './-overview-admin'

const styles = stylex.create({
  section: { display: 'flex', flexDirection: 'column', gap: unit.x4, marginTop: unit.x4 }
})

/**
 * The overview's body by audience: every account sees its own facts, and the
 * administrator sees the console's state above them, each under its own title
 * so the two never read as one page.
 */
export function OverviewSections({ administrator }: { administrator: boolean }) {
  if (!administrator) return <AccountOverview />
  return (
    <>
      <div {...stylex.props(styles.section)}>
        <Text render={<h2 />} variant='featured-6' weight='semibold'>
          Administration
        </Text>
        <AdminOverview />
      </div>
      <div {...stylex.props(styles.section)}>
        <Text render={<h2 />} variant='featured-6' weight='semibold'>
          Your account
        </Text>
        <AccountOverview />
      </div>
    </>
  )
}
