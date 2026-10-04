package authz

import (
	"slices"
	"testing"

	"github.com/riipandi/saka/framework/authz"
)

func TestTheCatalogCarriesEverySlugTheGrammarAllows(t *testing.T) {
	catalog := CatalogBySlug()
	if len(catalog) == 0 {
		t.Fatal("the catalog must not be empty")
	}
	for slug := range catalog {
		if !authz.ValidSlug(slug) {
			t.Errorf("catalog slug %q is not a slug the grammar accepts", slug)
		}
	}
	if _, ok := catalog["user:*:assign_role"]; !ok {
		t.Error("the user resource must carry assign_role")
	}
	for slug := range catalog {
		if slug == "user:usr_1:read" {
			t.Error("a per-instance slug is minted at the feature, never cataloged")
		}
	}
}

func TestTheAdministratorRoleHoldsTheWholeCatalog(t *testing.T) {
	for _, role := range SystemRoles {
		if role.Slug != AdministratorRole {
			continue
		}
		for _, slug := range AllSlugs() {
			if !slices.Contains(role.Permissions, slug) {
				t.Errorf("the administrator role must hold %q", slug)
			}
		}
	}
}
