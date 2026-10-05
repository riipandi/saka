package authz

import (
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

// TestAConstructedCatalogIsComposable builds the engine the way a second
// binary would: from its own resource table, with no other dependency in
// sight (decision 16).
func TestAConstructedCatalogIsComposable(t *testing.T) {
	catalog := NewCatalog([]Resource{
		{
			Name:        "widget",
			Actions:     []string{"read", "create"},
			Description: "a widget",
		},
		{
			Name:        "gadget",
			Actions:     []string{"read"},
			Description: "a gadget",
		},
	})

	if got, want := len(catalog.Permissions()), 3; got != want {
		t.Fatalf("Permissions() = %d entries, want %d", got, want)
	}
	if slug := catalog.Permissions()[0].Slug; slug != "widget:*:read" {
		t.Errorf("the first entry is %q, want widget:*:read", slug)
	}
	if _, ok := catalog.BySlug()["gadget:*:read"]; !ok {
		t.Error("BySlug must index every constructed resource")
	}
	slugs := catalog.AllSlugs()
	if len(slugs) != 3 || slugs[2] != "gadget:*:read" {
		t.Errorf("AllSlugs() = %v, want the catalog order", slugs)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("a duplicate slug must fail construction")
			}
		}()
		NewCatalog([]Resource{
			{Name: "widget", Actions: []string{"read"}},
			{Name: "widget", Actions: []string{"read"}},
		})
	}()
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
