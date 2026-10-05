// Package envfile reads and updates dotenv files while preserving
// comments, blank lines, and the original key order.
package envfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// DefaultMode is the permission of a newly created secrets file.
const DefaultMode fs.FileMode = 0o600

// DatabaseURL names the environment variable holding the Postgres connection
// string.
//
// It is spelled out here because pkg/ cannot import internal/: the config layer
// owns the key (database.url) and derives the same name through EnvName. A test
// in internal/config asserts the two agree, so the duplication cannot drift
// silently.
const DatabaseURL = "DATABASE_URL"

// File is an in-memory dotenv file.
type File struct {
	lines []string
	index map[string]int
}

// Load reads path into memory. A missing file yields an empty File so
// the caller can create it.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Parse(""), nil
		}
		return nil, fmt.Errorf("envfile: read %s: %w", path, err)
	}
	return Parse(string(raw)), nil
}

// Parse builds a File from dotenv content.
func Parse(content string) *File {
	file := &File{index: map[string]int{}}
	trimmed := strings.TrimSuffix(content, "\n")
	if trimmed == "" {
		return file
	}

	for line := range strings.SplitSeq(trimmed, "\n") {
		file.lines = append(file.lines, strings.TrimSuffix(line, "\r"))
		file.record(len(file.lines) - 1)
	}
	return file
}

// record maps a line to its key when the line defines one.
func (f *File) record(pos int) {
	key, ok := keyOf(f.lines[pos])
	if !ok {
		return
	}
	if _, exists := f.index[key]; !exists {
		f.index[key] = pos
	}
}

// keyOf extracts the variable name of a `KEY=value` line, accepting the
// `export ` prefix.
func keyOf(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	key, _, found := strings.Cut(trimmed, "=")
	key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
	if !found || key == "" {
		return "", false
	}
	return key, true
}

// Get returns the value of key.
func (f *File) Get(key string) (string, bool) {
	pos, ok := f.index[key]
	if !ok {
		return "", false
	}
	_, value, _ := strings.Cut(f.lines[pos], "=")
	return unquote(strings.TrimSpace(value)), true
}

// Keys returns the variable names in file order.
func (f *File) Keys() []string {
	keys := make([]string, 0, len(f.index))
	seen := make(map[string]bool, len(f.index))
	for _, line := range f.lines {
		key, ok := keyOf(line)
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys
}

// Set updates key in place or appends it when absent. It reports
// whether the key already existed.
func (f *File) Set(key, value string) bool {
	line := render(key, value)
	if pos, ok := f.index[key]; ok {
		f.lines[pos] = line
		return true
	}
	f.index[key] = len(f.lines)
	f.lines = append(f.lines, line)
	return false
}

// Render returns the dotenv content with a trailing newline.
func (f *File) Render() string {
	if len(f.lines) == 0 {
		return ""
	}
	return strings.Join(f.lines, "\n") + "\n"
}

// Write saves the file at path, creating it with DefaultMode and
// keeping the mode of an existing file.
func (f *File) Write(path string) error {
	mode := DefaultMode
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(f.Render()), mode); err != nil {
		return fmt.Errorf("envfile: write %s: %w", path, err)
	}
	return nil
}

// render formats one assignment, quoting values a dotenv parser could
// otherwise misread.
func render(key, value string) string {
	if needsQuote(value) {
		return key + `="` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
	}
	return key + "=" + value
}

// needsQuote reports whether value must be double-quoted to survive a
// dotenv round trip.
func needsQuote(value string) bool {
	if value == "" {
		return true
	}
	return strings.ContainsAny(value, " \t\"'#$`\\=")
}

// unquote reverses the quoting applied by render.
func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	if value[0] == '"' && value[len(value)-1] == '"' {
		inner := value[1 : len(value)-1]
		return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(inner)
	}
	if value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}
	return value
}
