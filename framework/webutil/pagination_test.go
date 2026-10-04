package webutil

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePaginationDefaults(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/users", nil)

	params, err := ParsePagination(r)

	require.NoError(t, err)
	assert.Equal(t, 1, params.Page)
	assert.Equal(t, DefaultPageSize, params.Limit)
	assert.Empty(t, params.SortBy)
	assert.Equal(t, SortAscending, params.SortOrder)
	assert.False(t, params.All())
	assert.Equal(t, 0, params.Offset())
}

func TestParsePaginationCustom(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/users?page=3&limit=50&sort_by=created_at&sort_order=desc", nil)

	params, err := ParsePagination(r)

	require.NoError(t, err)
	assert.Equal(t, 3, params.Page)
	assert.Equal(t, 50, params.Limit)
	assert.Equal(t, "created_at", params.SortBy)
	assert.Equal(t, SortDescending, params.SortOrder)
	assert.Equal(t, 100, params.Offset())
}

func TestParsePaginationAllRecords(t *testing.T) {
	for _, query := range []string{"?page=-1", "?limit=-1", "?page=-1&limit=-1"} {
		r := httptest.NewRequest("GET", "/api/users"+query, nil)

		params, err := ParsePagination(r)

		require.NoError(t, err, query)
		assert.True(t, params.All(), query)
		assert.Equal(t, 0, params.Offset(), query)
	}
}

func TestParsePaginationCapsLimit(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/users?limit=1000", nil)

	params, err := ParsePagination(r)

	require.NoError(t, err)
	assert.Equal(t, MaxPageSize, params.Limit)
}

func TestParsePaginationSized(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/users", nil)

	params, err := ParsePaginationSized(r, 10, 20)

	require.NoError(t, err)
	assert.Equal(t, 10, params.Limit)
}

func TestParsePaginationInvalid(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"zero page", "?page=0"},
		{"negative page", "?page=-2"},
		{"non-numeric page", "?page=abc"},
		{"zero limit", "?limit=0"},
		{"negative limit", "?limit=-2"},
		{"non-numeric limit", "?limit=abc"},
		{"invalid sort order", "?sort_order=upward"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/users"+tt.query, nil)

			_, err := ParsePagination(r)

			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidPagination))
		})
	}
}

func TestNewPaginationMiddlePage(t *testing.T) {
	p := NewPagination(PaginationParams{Page: 2, Limit: 10}, 35)

	assert.Equal(t, 2, *p.Page)
	assert.Equal(t, 10, *p.Limit)
	assert.Equal(t, 4, *p.TotalPages)
	assert.Equal(t, 35, *p.TotalItems)
	assert.Equal(t, 10, *p.FirstItemIndex)
	assert.Equal(t, 19, *p.LastItemIndex)
}

func TestNewPaginationPartialLastPage(t *testing.T) {
	p := NewPagination(PaginationParams{Page: 4, Limit: 10}, 35)

	assert.Equal(t, 30, *p.FirstItemIndex)
	assert.Equal(t, 34, *p.LastItemIndex)
}

func TestNewPaginationOutOfRangePage(t *testing.T) {
	p := NewPagination(PaginationParams{Page: 5, Limit: 10}, 35)

	assert.Equal(t, 5, *p.Page)
	assert.Nil(t, p.FirstItemIndex)
	assert.Nil(t, p.LastItemIndex)
}

func TestNewPaginationEmptyResult(t *testing.T) {
	p := NewPagination(PaginationParams{Page: 1, Limit: 10}, 0)

	assert.Equal(t, 0, *p.TotalPages)
	assert.Equal(t, 0, *p.TotalItems)
	assert.Nil(t, p.FirstItemIndex)
	assert.Nil(t, p.LastItemIndex)
}

func TestNewPaginationAllRecords(t *testing.T) {
	p := NewPagination(PaginationParams{Page: PaginationAll, Limit: 10}, 7)

	assert.Nil(t, p.Page)
	assert.Nil(t, p.Limit)
	assert.Nil(t, p.TotalPages)
	assert.Equal(t, 7, *p.TotalItems)
	assert.Equal(t, 0, *p.FirstItemIndex)
	assert.Equal(t, 6, *p.LastItemIndex)
}

func TestNewPaginationNegativeTotal(t *testing.T) {
	p := NewPagination(PaginationParams{Page: 1, Limit: 10}, -1)

	assert.Nil(t, p.Page)
	assert.Nil(t, p.TotalItems)
}
