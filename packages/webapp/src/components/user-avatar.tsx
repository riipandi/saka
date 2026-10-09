import { Blobatar } from '@blobatar/react'
import * as stylex from '@stylexjs/stylex'
import { Avatar, AvatarFallback, AvatarImage } from 'uilibs/components/base/avatar'
import type { AvatarSize } from 'uilibs/components/base/avatar'

const styles = stylex.create({
  fill: {
    height: '100%',
    width: '100%'
  }
})

export interface UserAvatarProps {
  /**
   * Who the avatar stands for — the account's display name or username.
   * It seeds the fallback blob and labels the picture for readers.
   */
  name: string
  /**
   * The account's picture URL, as the account view carries it. Empty —
   * the wire's "no picture" — renders the deterministic blobatar the
   * name seeds instead of the bundled default picture.
   */
  picture?: string | undefined
  size?: AvatarSize
  style?: stylex.StyleXStyles
}

/**
 * An account's avatar: the uploaded picture when the account has one, and
 * a deterministic geometric blob seeded by the name when it does not —
 * the same name always renders the same blob, so an account keeps its
 * face across screens without storing anything.
 */
export function UserAvatar({ name, picture, size = 'lg', style }: UserAvatarProps) {
  return (
    <Avatar size={size} style={style}>
      {picture ? <AvatarImage src={picture} alt={name} /> : null}
      <AvatarFallback>
        <Blobatar name={name} {...stylex.props(styles.fill)} />
      </AvatarFallback>
    </Avatar>
  )
}
