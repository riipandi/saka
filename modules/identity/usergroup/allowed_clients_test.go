package usergroup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/tango/internal/datastore"
)

// TestSetAllowedOidcClientsReplacesTheRollAndRefusesAnUnknownClient covers
// the group-side allowlist: the replacement is whole, an empty list empties
// the roll, and one identifier that names no client refuses the replacement
// whole — the group keeps the set it held.
func TestSetAllowedOidcClientsReplacesTheRollAndRefusesAnUnknownClient(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	group, err := service.CreateUserGroup(t.Context(), CreateParams{Name: "gryffindor", DisplayName: "Gryffindor"})
	require.NoError(t, err)

	// Two real clients, seeded beside the groups the junction joins.
	portal := seedOidcClient(t, pool, "hogwarts-portal", "Hogwarts Portal")
	library := seedOidcClient(t, pool, "hogwarts-library", "Hogwarts Library")

	var view GroupView
	view, allowed, err := service.SetAllowedOidcClients(t.Context(), FormatID(IDToUUID(group.ID)), []string{portal, library})
	require.NoError(t, err)
	require.Len(t, allowed, 2)
	assert.Equal(t, "Hogwarts Library", allowed[0].Name, "the roll answers ordered by name")
	assert.Equal(t, "Hogwarts Portal", allowed[1].Name)
	assert.Equal(t, "gryffindor", view.Name)

	// The replace, not a delta.
	_, allowed, err = service.SetAllowedOidcClients(t.Context(), FormatID(IDToUUID(group.ID)), []string{portal})
	require.NoError(t, err)
	require.Len(t, allowed, 1)
	assert.Equal(t, portal, allowed[0].ID)

	// An empty list empties the roll.
	_, allowed, err = service.SetAllowedOidcClients(t.Context(), FormatID(IDToUUID(group.ID)), nil)
	require.NoError(t, err)
	assert.Empty(t, allowed)

	// An unknown client refuses the replacement whole; the roll stays
	// empty — the state the refused replacement named nothing of.
	_, _, err = service.SetAllowedOidcClients(t.Context(), FormatID(IDToUUID(group.ID)), []string{"unknown-client"})
	assert.ErrorIs(t, err, ErrClientUnknown)
	_, allowed, err = service.SetAllowedOidcClients(t.Context(), FormatID(IDToUUID(group.ID)), []string{portal})
	require.NoError(t, err)
	require.Len(t, allowed, 1)
	_, allowed, err = service.SetAllowedOidcClients(t.Context(), FormatID(IDToUUID(group.ID)), nil)
	require.NoError(t, err)
	assert.Empty(t, allowed)

	// An unknown group is the not-found refusal.
	_, _, err = service.SetAllowedOidcClients(t.Context(), "ugrp_00000000000000000000000000", nil)
	assert.ErrorIs(t, err, ErrGroupNotFound)
}

// TestGroupDetailCarriesTheClientRoll pins the parity read: the group's
// detail view answers the allowlist the Set procedure wrote, ordered by
// name — the same roll the Set response carries, read back where upstream
// answers it.
func TestGroupDetailCarriesTheClientRoll(t *testing.T) {
	pool := migratedPool(t)
	service := testService(t, pool)

	group, err := service.CreateUserGroup(t.Context(), CreateParams{Name: "ravenclaw", DisplayName: "Ravenclaw"})
	require.NoError(t, err)
	portal := seedOidcClient(t, pool, "hogwarts-portal", "Hogwarts Portal")
	library := seedOidcClient(t, pool, "hogwarts-library", "Hogwarts Library")
	_, _, err = service.SetAllowedOidcClients(t.Context(), FormatID(IDToUUID(group.ID)), []string{portal, library})
	require.NoError(t, err)

	detail, err := service.GetGroup(t.Context(), FormatID(IDToUUID(group.ID)))
	require.NoError(t, err)
	require.Len(t, detail.AllowedClients, 2)
	assert.Equal(t, "Hogwarts Library", detail.AllowedClients[0].Name)
	assert.Equal(t, "Hogwarts Portal", detail.AllowedClients[1].Name)

	// The roll the detail answers is the junction's: an emptied allowlist
	// reads back as none.
	_, _, err = service.SetAllowedOidcClients(t.Context(), FormatID(IDToUUID(group.ID)), nil)
	require.NoError(t, err)
	detail, err = service.GetGroup(t.Context(), FormatID(IDToUUID(group.ID)))
	require.NoError(t, err)
	assert.Empty(t, detail.AllowedClients)
}

// seedOidcClient inserts an OIDC client row directly and answers its
// identifier — the allowlist's join target, another feature's table the
// test seeds the same way the junction's foreign keys expect.
func seedOidcClient(t *testing.T, pool *datastore.Postgres, id, name string) string {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.oidc_clients (id, name, callback_urls)
		VALUES ($1, $2, '[]'::jsonb)`, id, name)
	require.NoError(t, err)
	return id
}
