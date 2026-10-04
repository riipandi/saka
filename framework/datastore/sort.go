package datastore

// ListOrder resolves the ORDER BY expression a paged list answers with: the
// caller's choice among the whitelist, or the column the list reads
// naturally by, with the direction applied. The whitelist is the only path a
// sort key takes to SQL text, so a request can never order by an arbitrary
// column.
//
// The tiebreaker on the identifier is the caller's to append — the lists
// that need one pass it to `SelectBuilder.OrderBy` as the second key.
func ListOrder(columns map[string]string, sortBy, fallback string, ascending bool) string {
	order, ok := columns[sortBy]
	if !ok {
		order = columns[fallback]
	}
	if ascending {
		return order + " ASC"
	}
	return order + " DESC"
}
