package main

import (
	"fmt"
	"os"
	"strings"
)

// pass reports a scenario step that held.
func pass(format string, args ...any) { fmt.Println("  ok  " + fmt.Sprintf(format, args...)) }

// fail reports a step that refused and stops the run: the ladder's acts
// stand on each other, so a dead step ends the story.
func fail(format string, args ...any) {
	fmt.Println("FAIL  " + fmt.Sprintf(format, args...))
	os.Exit(1)
}

// str reads a field out of the answer document, dotted paths walking the
// nesting. A field the answer does not carry reads as empty.
func str(document map[string]any, field string) string {
	if !strings.Contains(field, ".") {
		value, _ := document[field].(string)
		return value
	}
	head, tail, _ := strings.Cut(field, ".")
	next, _ := document[head].(map[string]any)
	if next == nil {
		return ""
	}
	return str(next, tail)
}

func anyTrue(value any) bool { return value == true }
