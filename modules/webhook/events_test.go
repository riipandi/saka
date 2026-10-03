package webhook

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/audit"
)

// TestTheCatalogCoversEveryAuditEvent pins the catalog's completeness: the
// audit vocabulary and the wire mapping are two lists that must stay one
// set. An audit constant without a mapping is a happening no receiver can
// subscribe to; a mapping without a constant names nothing a record can
// carry.
func TestTheCatalogCoversEveryAuditEvent(t *testing.T) {
	mapped := make([]string, 0, len(catalog))
	for _, event := range catalog {
		if event.Source != "" {
			mapped = append(mapped, event.Source)
		}
	}
	declared := audit.Catalog()

	require.Len(t, mapped, len(declared), "the catalog maps every audit event exactly once")
	for _, source := range declared {
		assert.Contains(t, mapped, source.Name)
	}
}

// TestTheCatalogNamesAreUniqueDotNames pins the wire names' shape: unique,
// dot-separated lowercase segments, each carrying a description, with the
// test event among them. The names are the contract a receiver hardcodes,
// so the shape is pinned here and nowhere else.
func TestTheCatalogNamesAreUniqueDotNames(t *testing.T) {
	seen := map[string]bool{}
	for _, event := range catalog {
		require.False(t, seen[event.Name], "the catalog carries %q twice", event.Name)
		seen[event.Name] = true
		assert.Regexp(t, `^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`, event.Name)
		assert.NotEqual(t, "*", event.Name, "the wildcard is not a catalog entry")
		assert.NotEmpty(t, event.Description)
	}
	assert.Contains(t, seen, EventTest, "the test event is subscribable like any other")
}

// TestCheckEventsRefusesNamesOutsideTheCatalog pins the subscription
// boundary at the helper every procedure runs through.
func TestCheckEventsRefusesNamesOutsideTheCatalog(t *testing.T) {
	require.NoError(t, checkEvents(nil))
	require.NoError(t, checkEvents([]string{"*"}))
	require.NoError(t, checkEvents([]string{"user.created", "api_key.revoked"}))

	err := checkEvents([]string{"user.created", "user_created"})
	require.ErrorIs(t, err, ErrUnknownEvent)

	require.NoError(t, checkEventFilter(""))
	require.NoError(t, checkEventFilter("*"))
	require.ErrorIs(t, checkEventFilter("sign_in"), ErrUnknownEvent)
}

// TestCreateRejectsAnUnknownEvent pins the boundary on the write path: a
// registration or an update naming an event the catalog does not declare is
// refused, not stored to deliver nothing.
func TestCreateRejectsAnUnknownEvent(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool, nil)

	_, _, err := service.Create(t.Context(), CreateParams{
		Name:       "Langdon",
		Endpoint:   "https://example.com/hook",
		Method:     http.MethodPost,
		EventTypes: []string{"user_created"},
	})
	assert.ErrorIs(t, err, ErrUnknownEvent)

	row, _, err := service.Create(t.Context(), CreateParams{
		Name:       "Neveu",
		Endpoint:   "https://example.com/hook",
		Method:     http.MethodPost,
		EventTypes: []string{"user.created"},
	})
	require.NoError(t, err)

	wrong := "user.created"
	_, err = service.Update(t.Context(), row.ID, UpdateParams{EventTypes: &[]string{wrong, "user_created"}})
	assert.ErrorIs(t, err, ErrUnknownEvent)
}
