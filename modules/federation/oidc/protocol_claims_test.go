package oidc

import (
	"testing"

	"github.com/luikyv/go-oidc/pkg/goidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/modules/identity/user"
)

// The issuance-side defense against legacy reserved keys: the write side
// refuses them, but a row written before that rule still must not reach a
// token — the merge drops every claim whose key the protocol mints itself.

func TestLegacyReservedClaimsAreDroppedAtIssuance(t *testing.T) {
	pool := migratedPool(t)
	account := seedAccount(t, pool, "hermione")
	accountWire, err := user.IDFromUUIDString(account.String())
	require.NoError(t, err)

	service := testService(t, pool).WithUserDirectory(&stubDirectory{
		accounts: map[string]user.UserView{
			accountWire.String(): {
				ID:       accountWire.String(),
				Username: "hermione",
				Email:    "hermione@hogwarts.example",
			},
		},
	}).WithClaimSource(&stubClaims{
		// A legacy row pretending to be the subject or the email, beside
		// one ordinary claim that must survive.
		userClaims: []Claim{
			{Key: "sub", Value: "evil"},
			{Key: "email", Value: "evil@hogwarts.example"},
			{Key: "house", Value: "gryffindor"},
		},
		groupClaims: []Claim{
			{Key: "groups", Value: `["evil"]`},
		},
	})

	grant := &goidc.Grant{Subject: account.String(), Scopes: "openid profile email"}
	result := subjectClaims(t.Context(), service, service.claims, grant)

	// The protected claims stay the protocol's own.
	_, hasSub := result["sub"]
	assert.False(t, hasSub, "a custom claim must not replace the subject")
	assert.Equal(t, "hermione@hogwarts.example", result["email"], "the account's email wins over the claim row")
	_, hasGroups := result["groups"]
	assert.False(t, hasGroups, "a custom claim must not replace the group names")

	// The ordinary claim rides as before.
	assert.Equal(t, "gryffindor", result["house"])
}
