package seeders_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/database/seeders"
)

// The announcement leads the report; the system notice, its audience
// junction, and the two read receipts follow.
func TestNotificationSeederPublishesTheDevelopmentNotices(t *testing.T) {
	pool := newSeededPool(t)

	results := runSeeders(t, pool, false)

	require.Len(t, results, 8)
	require.Equal(t, seeders.NotificationSeederName, results[4].Name)
	require.Len(t, results[4].Created, 5)
	assert.Empty(t, results[4].Skipped)

	count := countRows(t, pool, `SELECT count(*) FROM public.notifications`)
	assert.Equal(t, 2, count)

	// The announcement reaches everyone; the system notice names the
	// editors group and carries no topic, the way the checks police it.
	assert.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM public.notifications
		WHERE category = 'announcement' AND audience_kind = 'global' AND topic = 'Maintenance'`))
	assert.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM public.notifications n
		JOIN public.notification_user_groups nug ON nug.notification_id = n.id
		JOIN public.user_groups g ON g.id = nug.user_group_id
		WHERE n.category = 'system' AND g.name = 'editors' AND n.topic IS NULL`))

	// One editor has read the notice and the other has not: the inbox
	// renders both states without a second notice.
	assert.Equal(t, 2, countRows(t, pool, `SELECT count(*) FROM public.notification_reads`))
	assert.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM public.notification_reads r
		JOIN public.users u ON u.id = r.user_id WHERE u.username = 'hermione_granger'`))
	assert.Equal(t, 0, countRows(t, pool, `SELECT count(*) FROM public.notification_reads r
		JOIN public.users u ON u.id = r.user_id WHERE u.username = 'robert_langdon'`))
}

func TestNotificationSeederIsIdempotent(t *testing.T) {
	pool := newSeededPool(t)
	runSeeders(t, pool, false)

	results := runSeeders(t, pool, false)

	require.Equal(t, seeders.NotificationSeederName, results[4].Name)
	assert.Empty(t, results[4].Created)
	assert.Len(t, results[4].Skipped, 5)
	assert.Equal(t, 2, countRows(t, pool, `SELECT count(*) FROM public.notifications`))
	assert.Equal(t, 2, countRows(t, pool, `SELECT count(*) FROM public.notification_reads`))
}
