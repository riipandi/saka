import type { Meta, StoryObj } from '@storybook/tanstack-react'
import atoms from '@stylexjs/atoms'
import * as stylex from '@stylexjs/stylex'
import { expect } from 'storybook/test'
import { colors } from '../../../styles/colors.stylex'
import { Button } from '../../base/button'
import { Badge } from '../badge'
import { Spinner } from './spinner.component'

const meta = {
  title: 'Extra Components/Spinner',
  component: Spinner,
  parameters: { layout: 'centered' },
  tags: [], // ['autodocs']
  decorators: [
    (Story) => (
      <div
        {...stylex.props(
          atoms.display.flex,
          atoms.justifyContent.center,
          atoms.padding['20px'],
          atoms.minWidth['448px'],
          atoms.width['100%']
        )}
      >
        <Story />
      </div>
    )
  ]
} satisfies Meta<typeof Spinner>

type Story = StoryObj<typeof meta>

const styles = stylex.create({
  iconXs: { height: 14, width: 14 },
  iconLg: { height: 24, width: 24 },
  spinnerSm: { height: 12, width: 12 },
  row: {
    alignItems: 'center',
    display: 'flex',
    gap: 16
  },
  stack: {
    alignItems: 'flex-start',
    display: 'flex',
    flexDirection: 'column',
    gap: 16
  },
  line: {
    alignItems: 'center',
    color: colors.foregroundNeutralFaded,
    display: 'flex',
    gap: 8,
    margin: 0
  }
})

export default meta

export const Playground: Story = {
  render: () => (
    <Button disabled>
      <Spinner /> Divining…
    </Button>
  ),
  // The spinner svg announces itself, so the button name combines both labels.
  play: async ({ canvas }) => {
    const button = canvas.getByRole('button', { name: /Divining/ })
    await expect(button).toBeDisabled()
    await expect(button.querySelector('svg')).not.toBeNull()
  }
}

export const Sizes: Story = {
  render: () => (
    <div {...stylex.props(styles.row)}>
      <Spinner style={styles.iconXs} />
      <Spinner />
      <Spinner style={styles.iconLg} />
    </div>
  )
}

export const Composition: Story = {
  render: () => (
    <div {...stylex.props(styles.stack)}>
      <Badge variant='secondary'>
        <Spinner {...stylex.props(styles.spinnerSm)} />
        Brewing
      </Badge>
      <p {...stylex.props(styles.line)}>
        <Spinner />
        Decoding the cryptex…
      </p>
    </div>
  )
}
