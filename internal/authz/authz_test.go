package authz

import (
	"slices"
	"testing"
)

func TestMatchHonorsTheWildcardOnlyInTheInstancePosition(t *testing.T) {
	tests := []struct {
		name        string
		requirement string
		grant       string
		want        bool
	}{
		{"exact grant", "user:usr_1:read", "user:usr_1:read", true},
		{"wildcard instance", "user:usr_1:read", "user:*:read", true},
		{"different instance", "user:usr_1:read", "user:usr_2:read", false},
		{"different resource", "user:usr_1:read", "session:usr_1:read", false},
		{"different action", "user:usr_1:read", "user:usr_1:update", false},
		{"wildcard action is not a grant", "user:usr_1:read", "user:usr_1:*", false},
		{"wildcard resource is not a grant", "user:usr_1:read", "*:*:read", false},
		{"two-part slug", "user:read", "user:*:read", false},
		{"empty segments", "user::read", "user:*:read", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Match(test.requirement, test.grant); got != test.want {
				t.Errorf("Match(%q, %q) = %v, want %v", test.requirement, test.grant, got, test.want)
			}
		})
	}
}

func TestGrantsAnswersAcrossTheHeldSet(t *testing.T) {
	held := []string{"notification:*:read", "user:usr_1:ban"}
	if !Grants(held, "notification:ntf_9:read") {
		t.Error("a wildcard grant must satisfy the instance requirement")
	}
	if !Grants(held, "user:usr_1:ban") {
		t.Error("an exact grant must satisfy itself")
	}
	if Grants(held, "user:usr_2:ban") {
		t.Error("a single-instance grant must not satisfy another instance")
	}
	if Grants(nil, "user:*:read") {
		t.Error("an empty held set must satisfy nothing")
	}
}

func TestTheCatalogCarriesEverySlugTheGrammarAllows(t *testing.T) {
	catalog := CatalogBySlug()
	if len(catalog) == 0 {
		t.Fatal("the catalog must not be empty")
	}
	for slug := range catalog {
		if !ValidSlug(slug) {
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

func TestValidSlugRejectsWhatTheGrammarDoesNotDeclare(t *testing.T) {
	invalid := []string{
		"",
		"user",
		"user:read",
		"user:a:b:c",
		"user:*:*",
		"*:*:read",
		"user:usr_1:*",
		"user:*:READ",
		"user:*:re ad",
		"user:*:read:",
		":*:",
	}
	for _, slug := range invalid {
		if ValidSlug(slug) {
			t.Errorf("ValidSlug(%q) = true, want false", slug)
		}
	}
	for _, slug := range []string{"user:*:read", "user:usr_1:read", "audit_log:*:filter_options"} {
		if !ValidSlug(slug) {
			t.Errorf("ValidSlug(%q) = false, want true", slug)
		}
	}
}
