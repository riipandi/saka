package oidc

import (
	"testing"
	"time"

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
			{Key: "updated_at", Value: "the day before yesterday"},
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

// TestTheProfileScopeAnswersTheStandardPictureAndChangeStamp pins the two
// Standard Claims the account read-model carries: the picture answers the
// composed public URL under its standard name, and the change stamp answers
// epoch seconds — the trigger's stamp when the account has one, the creation
// stamp when it has none. Both ride the profile scope alone.
func TestTheProfileScopeAnswersTheStandardPictureAndChangeStamp(t *testing.T) {
	pool := migratedPool(t)
	account := seedAccount(t, pool, "vittoria")
	accountWire, err := user.IDFromUUIDString(account.String())
	require.NoError(t, err)

	updated := time.Unix(1759500000, 0).UTC()
	created := time.Unix(1750000000, 0).UTC()
	service := testService(t, pool).WithUserDirectory(&stubDirectory{
		accounts: map[string]user.UserView{
			accountWire.String(): {
				ID:        accountWire.String(),
				Username:  "vittoria",
				Email:     "vittoria@hogwarts.example",
				Picture:   "https://app.hogwarts.example/storage/pictures/hermione.png",
				CreatedAt: created,
				UpdatedAt: &updated,
			},
		},
	})

	grant := &goidc.Grant{Subject: account.String(), Scopes: "openid profile email"}
	result := subjectClaims(t.Context(), service, service.claims, grant)
	assert.Equal(t, "https://app.hogwarts.example/storage/pictures/hermione.png", result["picture"])
	assert.Equal(t, updated.Unix(), result["updated_at"])

	// The narrower grant answers neither: the claims ride the profile
	// scope the §5.4 mapping names.
	narrow := &goidc.Grant{Subject: account.String(), Scopes: "openid email"}
	result = subjectClaims(t.Context(), service, service.claims, narrow)
	_, hasPicture := result["picture"]
	_, hasUpdated := result["updated_at"]
	assert.False(t, hasPicture)
	assert.False(t, hasUpdated)

	// The account no update has touched answers the creation stamp — its
	// information last changed when it was created.
	service = testService(t, pool).WithUserDirectory(&stubDirectory{
		accounts: map[string]user.UserView{
			accountWire.String(): {
				ID:        accountWire.String(),
				Username:  "vittoria",
				Email:     "vittoria@hogwarts.example",
				CreatedAt: created,
			},
		},
	})
	result = subjectClaims(t.Context(), service, service.claims, grant)
	_, hasPicture = result["picture"]
	assert.False(t, hasPicture, "an unpictured account answers no picture claim")
	assert.Equal(t, created.Unix(), result["updated_at"])
}

// TestTheOperatorRowCannotShadowTheChangeStamp extends the legacy-reserved
// defense to updated_at: the protocol mints it, so an operator's custom claim
// row naming it is dropped at issuance.
func TestTheOperatorRowCannotShadowTheChangeStamp(t *testing.T) {
	pool := migratedPool(t)
	account := seedAccount(t, pool, "robert")
	accountWire, err := user.IDFromUUIDString(account.String())
	require.NoError(t, err)

	created := time.Unix(1750000000, 0).UTC()
	service := testService(t, pool).WithUserDirectory(&stubDirectory{
		accounts: map[string]user.UserView{
			accountWire.String(): {
				ID:        accountWire.String(),
				Username:  "robert",
				Email:     "robert@hogwarts.example",
				CreatedAt: created,
			},
		},
	}).WithClaimSource(&stubClaims{
		userClaims: []Claim{
			{Key: "updated_at", Value: "evil"},
			{Key: "picture", Value: "evil.png"},
			{Key: "motto", Value: "fire and water"},
		},
	})

	grant := &goidc.Grant{Subject: account.String(), Scopes: "openid profile"}
	result := subjectClaims(t.Context(), service, service.claims, grant)
	assert.Equal(t, created.Unix(), result["updated_at"], "the protocol's stamp wins")
	_, hasPicture := result["picture"]
	assert.False(t, hasPicture, "the account carries no picture; the operator's row does not mint one")
	assert.Equal(t, "fire and water", result["motto"])
}
