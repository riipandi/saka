package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

// The catalog's completeness is a source invariant, not a runtime one: a
// constant declared in audit.go without a catalog entry is an event a
// webhook can never be subscribed to, and an entry without a constant names
// nothing a record can carry. The test reads the constant block and holds
// the two lists to the same set, in the same order.
func TestCatalogCoversEveryEventConstant(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "audit.go", nil, 0)
	if err != nil {
		t.Fatalf("parse audit.go: %v", err)
	}

	type declared struct {
		constName string
		eventName string
	}
	var declareds []declared
	var seen = map[string]string{} // event name -> const name
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || len(valueSpec.Values) != 1 {
				continue
			}
			literal, ok := valueSpec.Values[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			name := valueSpec.Names[0].Name
			if len(name) <= len("Event") || name[:len("Event")] != "Event" {
				continue
			}
			value := literal.Value[1 : len(literal.Value)-1]
			declareds = append(declareds, declared{constName: name, eventName: value})
			if previous, dup := seen[value]; dup {
				t.Errorf("%s and %s both declare the event %q", previous, name, value)
			}
			seen[value] = name
		}
	}
	if len(declareds) == 0 {
		t.Fatal("no Event* string constants were found in audit.go")
	}

	catalogNames := make([]string, 0, len(catalog))
	for _, event := range catalog {
		catalogNames = append(catalogNames, event.Name)
		if event.Description == "" {
			t.Errorf("catalog entry %q carries no description", event.Name)
		}
	}
	if deduped := slices.Compact(slices.Sorted(slices.Values(catalogNames))); len(deduped) != len(catalogNames) {
		t.Error("the catalog carries a duplicate name")
	}

	missing := 0
	for i, declaredEvent := range declareds {
		if catalogNames[i] != declaredEvent.eventName {
			t.Errorf("catalog position %d holds %q, but %s declares %q — keep the catalog in the constants' declaration order",
				i, catalogNames[i], declaredEvent.constName, declaredEvent.eventName)
			missing++
		}
	}
	if missing > 0 {
		t.Errorf("%d catalog entries are out of order or missing", missing)
	}
}
