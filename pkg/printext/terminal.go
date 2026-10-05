package printext

import (
	"os"

	"golang.org/x/term"
)

// IsTerminal reports whether w is a terminal, so a caller can decide whether
// output may be redrawn in place or decorated.
//
// Only an *os.File can be a terminal. A bytes.Buffer, which is what a test
// passes, never is, so a test sees the plain rendering without asking for it.
func IsTerminal(w interface{ Write([]byte) (int, error) }) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
