import * as stylex from '@stylexjs/stylex'
import { Button } from 'uilibs/components/base/button'
import { Text } from 'uilibs/components/extra/text'

/**
 * The failed-list answer: the refusal's words and the retry the query
 * already owns. A bare red line left the reader stranded — the refetch was
 * always there, it just had no way to be asked for.
 */
export function ListError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div {...stylex.props(listErrorStyles.row)}>
      <Text render={<p />} variant='body-2' color='critical'>
        {message}
      </Text>
      <Button size='sm' variant='outline' onClick={onRetry}>
        Try again
      </Button>
    </div>
  )
}

const listErrorStyles = stylex.create({
  row: {
    alignItems: 'center',
    display: 'flex',
    gap: 12,
    justifyContent: 'center',
    paddingBlock: 24,
    width: '100%'
  }
})
