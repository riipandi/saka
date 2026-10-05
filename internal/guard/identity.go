package guard

import "reflect"

// IsPublic reports whether a rule is the public rule.
//
// A function value is not comparable in Go, so identity is tested through the
// pointer a func value carries in its data word, which is the function's code
// address: two references to the same top-level function carry the same
// pointer.
//
// This is what lets the public lists be derived from the tables instead of
// written beside them — the alternative, a second hand-kept list, is the
// drift the derivation exists to prevent.
func IsPublic(rule Rule) bool {
	return rule != nil && reflect.ValueOf(rule).Pointer() == reflect.ValueOf(Public).Pointer()
}
