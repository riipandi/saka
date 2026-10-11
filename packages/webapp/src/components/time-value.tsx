import * as stylex from '@stylexjs/stylex'
import { Tooltip, TooltipContent, TooltipTrigger } from 'uilibs/components/base/tooltip'
import { dataTableStyles } from '#/styles/element/data-table.stylex'

interface WireInstant {
  seconds: bigint
  nanos?: number
}

const absoluteFormat = new Intl.DateTimeFormat('en-GB', {
  dateStyle: 'medium',
  timeStyle: 'short'
})

function relative(at: Date): string {
  const seconds = Math.round((at.getTime() - Date.now()) / 1000)
  const span = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
  const table: Array<[Intl.RelativeTimeFormatUnit, number]> = [
    ['year', 60 * 60 * 24 * 365],
    ['month', 60 * 60 * 24 * 30],
    ['day', 60 * 60 * 24],
    ['hour', 60 * 60],
    ['minute', 60]
  ]
  for (const [unit, size] of table) {
    if (Math.abs(seconds) >= size) return span.format(Math.round(seconds / size), unit)
  }
  return span.format(seconds, 'second')
}

/** A relative instant; the tooltip is the absolute wall time. */
export function TimeValue({ value }: { value?: WireInstant | string | null }) {
  if (!value) return '—'
  const at =
    typeof value === 'string'
      ? new Date(value)
      : new Date(Number(value.seconds) * 1000 + Math.floor((value.nanos ?? 0) / 1e6))
  if (Number.isNaN(at.getTime())) return '—'
  const exact = absoluteFormat.format(at)
  return (
    <Tooltip>
      <TooltipTrigger {...stylex.props(dataTableStyles.instant)}>{relative(at)}</TooltipTrigger>
      <TooltipContent>{exact}</TooltipContent>
    </Tooltip>
  )
}
