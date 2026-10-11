import type { MouseEvent } from 'react'
import {
  Pagination,
  PaginationContent,
  PaginationEllipsis,
  PaginationItem,
  PaginationLink,
  PaginationNext,
  PaginationPrevious
} from 'uilibs/components/extra/pagination'

interface ListPaginationProps {
  page: number
  totalPages: number | null
  hasPrevious: boolean
  hasNext: boolean | null
  onPageChange: (page: number) => void
}

/** Page numbers around the current page, with ellipses when the run is long. */
function windowOf(page: number, total: number): Array<number | 'gap'> {
  if (total <= 7) return Array.from({ length: total }, (_, index) => index + 1)
  const pages: Array<number | 'gap'> = [1]
  const start = Math.max(2, page - 1)
  const end = Math.min(total - 1, page + 1)
  if (start > 2) pages.push('gap')
  for (let current = start; current <= end; current += 1) pages.push(current)
  if (end < total - 1) pages.push('gap')
  pages.push(total)
  return pages
}

/**
 * The list pager. Previous and next follow the summary the backend
 * answered; numbered links appear only when the total page count is known.
 */
export function ListPagination({
  page,
  totalPages,
  hasPrevious,
  hasNext,
  onPageChange
}: ListPaginationProps) {
  const go = (event: MouseEvent, next: number) => {
    event.preventDefault()
    onPageChange(next)
  }
  const numbers = totalPages !== null && totalPages > 1 ? windowOf(page, totalPages) : []

  return (
    <Pagination>
      <PaginationContent>
        <PaginationItem>
          <PaginationPrevious
            href={hasPrevious ? '#previous' : undefined}
            aria-disabled={!hasPrevious}
            onClick={(event) => {
              if (!hasPrevious) {
                event.preventDefault()
                return
              }
              go(event, page - 1)
            }}
          />
        </PaginationItem>
        {numbers.map((entry, index) =>
          entry === 'gap' ? (
            <PaginationItem key={`gap-${index}`}>
              <PaginationEllipsis />
            </PaginationItem>
          ) : (
            <PaginationItem key={entry}>
              <PaginationLink
                href={`#page-${entry}`}
                isActive={entry === page}
                onClick={(event) => go(event, entry)}
              >
                {entry}
              </PaginationLink>
            </PaginationItem>
          )
        )}
        <PaginationItem>
          <PaginationNext
            href={hasNext ? '#next' : undefined}
            aria-disabled={hasNext !== true}
            onClick={(event) => {
              if (hasNext !== true) {
                event.preventDefault()
                return
              }
              go(event, page + 1)
            }}
          />
        </PaginationItem>
      </PaginationContent>
    </Pagination>
  )
}
