package logger

import (
	"io"
	"os"

	"golang.org/x/term"
)

// isTerminal reports whether w is a terminal, so the console renderer knows
// whether it may write escape codes.
//
// The decision is per destination, not per process, the way pkg/printext makes
// it: a redirected run and a test buffer must stay plain text. Only an *os.File
// can be a terminal, so a buffer is never one.
func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
