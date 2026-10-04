package webutil

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// PaginationAll requests all records.
const PaginationAll = -1

// Default and maximum page sizes.
const (
	DefaultPageSize = 25
	MaxPageSize     = 100
)

// SortOrder is the direction of a sorted result set.
type SortOrder string

const (
	SortAscending  SortOrder = "asc"
	SortDescending SortOrder = "desc"
)

// ErrInvalidPagination is returned by ParsePagination for malformed
// pagination query parameters.
var ErrInvalidPagination = errors.New("invalid pagination parameters")

// PaginationParams holds pagination query parameters.
type PaginationParams struct {
	Page      int
	Limit     int
	SortBy    string
	SortOrder SortOrder
}

// All reports whether the params request every record.
func (p PaginationParams) All() bool {
	return p.Page == PaginationAll || p.Limit == PaginationAll
}

// Offset returns the SQL offset for the current page.
func (p PaginationParams) Offset() int {
	if p.All() || p.Page < 1 || p.Limit < 1 {
		return 0
	}
	return (p.Page - 1) * p.Limit
}

// NormalizePage applies the shared pagination rules to raw page and
// limit values and returns the effective pair: a page below 1 becomes
// the first page, a limit below 1 takes defaultLimit, and a limit above
// maxLimit is clamped. A transport that can express "unset" separately
// from zero (query parameters) rejects the zero before calling; a
// transport that cannot (proto3 int32) relies on the default here, so
// an unset limit never reaches a store as "no LIMIT".
func NormalizePage(page, limit, defaultLimit, maxLimit int) (int, int) {
	if page != PaginationAll && page < 1 {
		page = 1
	}
	switch {
	case limit == PaginationAll:
	case limit < 1:
		limit = defaultLimit
	case maxLimit > 0 && limit > maxLimit:
		limit = maxLimit
	}
	return page, limit
}

// All reports whether a normalized page and limit select every record.
func All(page, limit int) bool {
	return page == PaginationAll || limit == PaginationAll
}

// Offset returns the SQL offset for a page and limit.
func Offset(page, limit int) int {
	if All(page, limit) || page < 1 || limit < 1 {
		return 0
	}
	return (page - 1) * limit
}

// ItemRange returns the zero-based inclusive range of items a page
// covers for totalItems records. ok is false when the range is unknown
// (every record requested, an empty result, or an out-of-range page).
func ItemRange(page, limit, totalItems int) (first, last int, ok bool) {
	if totalItems < 0 {
		return 0, 0, false
	}
	if All(page, limit) || limit < 1 {
		if totalItems == 0 {
			return 0, 0, false
		}
		return 0, totalItems - 1, true
	}
	start := (page - 1) * limit
	if page < 1 || start >= totalItems {
		return 0, 0, false
	}
	return start, min(start+limit-1, totalItems-1), true
}

// ParsePagination reads pagination parameters from the request.
func ParsePagination(r *http.Request) (PaginationParams, error) {
	return ParsePaginationSized(r, DefaultPageSize, MaxPageSize)
}

// ParsePaginationSized reads pagination parameters with custom limits.
func ParsePaginationSized(r *http.Request, defaultLimit, maxLimit int) (PaginationParams, error) {
	q := r.URL.Query()
	params := PaginationParams{Page: 1, Limit: defaultLimit, SortOrder: SortAscending}

	if raw := strings.TrimSpace(q.Get("page")); raw != "" {
		page, err := strconv.Atoi(raw)
		if err != nil || (page != PaginationAll && page < 1) {
			return params, fmt.Errorf("%w: page must be a positive integer or -1", ErrInvalidPagination)
		}
		params.Page = page
	}

	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || (limit != PaginationAll && limit < 1) {
			return params, fmt.Errorf("%w: limit must be a positive integer or -1", ErrInvalidPagination)
		}
		if limit != PaginationAll && limit > maxLimit {
			limit = maxLimit
		}
		params.Limit = limit
	}

	params.SortBy = strings.TrimSpace(q.Get("sort_by"))

	if raw := strings.TrimSpace(q.Get("sort_order")); raw != "" {
		switch SortOrder(raw) {
		case SortAscending, SortDescending:
			params.SortOrder = SortOrder(raw)
		default:
			return params, fmt.Errorf("%w: sort_order must be %q or %q", ErrInvalidPagination, SortAscending, SortDescending)
		}
	}

	return params, nil
}

// Pagination describes a page within a result set.
type Pagination struct {
	Page           *int
	Limit          *int
	TotalPages     *int
	TotalItems     *int
	FirstItemIndex *int
	LastItemIndex  *int
}

// NewPagination computes page metadata for totalItems records.
func NewPagination(params PaginationParams, totalItems int) Pagination {
	p := Pagination{}
	if totalItems < 0 {
		return p
	}
	p.TotalItems = &totalItems

	page, limit := params.Page, params.Limit
	if params.All() || params.Limit < 1 {
		page, limit = PaginationAll, PaginationAll
	}
	if first, last, ok := ItemRange(page, limit, totalItems); ok {
		p.FirstItemIndex, p.LastItemIndex = &first, &last
	}
	if All(page, limit) {
		return p
	}

	p.Page = &page
	p.Limit = &limit
	p.TotalPages = new((totalItems + limit - 1) / limit)
	return p
}

func (m *Metadata) applyPagination(p Pagination) {
	m.Page = p.Page
	m.Limit = p.Limit
	m.TotalPages = p.TotalPages
	m.TotalItems = p.TotalItems
	m.FirstItemIndex = p.FirstItemIndex
	m.LastItemIndex = p.LastItemIndex
}
