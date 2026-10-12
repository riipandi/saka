import * as stylex from '@stylexjs/stylex'
import { Skeleton } from 'uilibs/components/extra/skeleton'
import { skeletonStyles as s } from '#/styles/element/skeletons.stylex'

/**
 * The loading shape of a card grid: a title line and two body lines inside
 * a bordered block, repeated across the same responsive columns the real
 * cards settle into (2 by medium, 3 by large).
 */
export function CardsSkeleton({ count = 3 }: { count?: number }) {
  return (
    <div
      aria-hidden
      data-testid='cards-skeleton'
      {...stylex.props(s.cardsGrid, s.cardsGridMedium, s.cardsGridLarge, s.cardsGridXLarge)}
    >
      {Array.from({ length: count }, (_, card) => (
        <div key={card} {...stylex.props(s.cardBlock)}>
          <Skeleton {...stylex.props(s.cardTitle)} />
          <Skeleton {...stylex.props(s.cardLine)} />
          <Skeleton {...stylex.props(s.cardLineShort)} />
        </div>
      ))}
    </div>
  )
}
