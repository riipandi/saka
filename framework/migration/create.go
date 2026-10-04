//go:build debug

package migration

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
)

// migrationFileMode is the mode of a generated migration. Git records only the
// executable bit, so this does not leak into a clone.
const migrationFileMode fs.FileMode = 0o644

// migrationTemplate is the skeleton a new migration starts from. Both blocks
// hold only a comment on purpose: goose reports a migration with no statements
// as "empty", which migrate:up prints, whereas a placeholder statement would
// look like a migration that ran and changed nothing.
const migrationTemplate = `-- +goose Up
-- +goose StatementBegin

-- Write the up migration here.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Write the down migration here.

-- +goose StatementEnd
`

var (
	// ErrInvalidMigrationName is returned when a name holds nothing that can
	// be used in a file name.
	ErrInvalidMigrationName = errors.New("database: migration name must contain at least one letter or digit")

	// ErrMigrationNameTaken is returned when another migration already uses
	// the same name, whatever its version.
	ErrMigrationNameTaken = errors.New("database: migration name is already taken")

	// ErrMigrationsDirMissing is returned when the target directory does not
	// exist. The command creates files, never directories.
	ErrMigrationsDirMissing = errors.New("database: migrations directory not found")

	// ErrMigrationVersionOverflow is returned when the next version would no
	// longer fit the version prefix, which every migration file must share.
	ErrMigrationVersionOverflow = errors.New("database: no version left in the migration prefix")
)

// CreateOptions describes the migration to write.
type CreateOptions struct {
	// Dir is the directory to write into. Required.
	Dir string
	// Name is the name as typed, in any casing or separator style.
	Name string
}

// CreatedMigration is the result of a successful create.
type CreatedMigration struct {
	// Version is the version the new file claims.
	Version int64
	// Name is the normalized name, as it appears in the file name.
	Name string
	// Path is the file that was written.
	Path string
}

// CreateMigration writes a migration skeleton into the target directory.
//
// The version is one past the highest version on disk, and the name is checked
// against every name already in use, so two migrations can never share a name
// or a version. Nothing is written when a check fails.
func CreateMigration(opts CreateOptions) (CreatedMigration, error) {
	dir := opts.Dir
	if dir == "" {
		return CreatedMigration{}, ErrMigrationsDirMissing
	}

	name := slugify(opts.Name)
	if name == "" {
		return CreatedMigration{}, ErrInvalidMigrationName
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return CreatedMigration{}, fmt.Errorf("%w: %s", ErrMigrationsDirMissing, dir)
		}
		return CreatedMigration{}, fmt.Errorf("database: read %s: %w", dir, err)
	}

	inventory := newInventory(entries)
	if taken, ok := inventory.names[name]; ok {
		return CreatedMigration{}, fmt.Errorf("%w: %s is used by %s",
			ErrMigrationNameTaken, name, taken)
	}

	version := inventory.highest + 1
	if len(strconv.FormatInt(version, 10)) > MigrationPrefixWidth {
		return CreatedMigration{}, fmt.Errorf("%w: version %d needs more than %d digits",
			ErrMigrationVersionOverflow, version, MigrationPrefixWidth)
	}
	path := filepath.Join(dir, fmt.Sprintf("%0*d_%s.sql", MigrationPrefixWidth, version, name))

	if err := os.WriteFile(path, []byte(migrationTemplate), migrationFileMode); err != nil {
		return CreatedMigration{}, fmt.Errorf("database: write %s: %w", path, err)
	}
	return CreatedMigration{Version: version, Name: name, Path: path}, nil
}

// migrationInventory is what the existing files in a directory tell us: which
// names are taken, and the highest version in use.
type migrationInventory struct {
	// names maps a normalized migration name to the file that uses it.
	names map[string]string
	// highest is the highest version any file claims.
	highest int64
}

// newInventory reads the directory entries. Every file claims its normalized
// name, so a migration can never shadow a file the directory already holds —
// including one goose cannot parse, which would otherwise sit next to a new
// file with the same name. Only parsable files contribute a version, because a
// name goose ignores must not inflate the sequence.
func newInventory(entries []os.DirEntry) migrationInventory {
	inventory := migrationInventory{names: make(map[string]string, len(entries))}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := migrationName(entry.Name())
		if name != "" {
			inventory.names[name] = entry.Name()
		}
		version, err := goose.NumericComponent(entry.Name())
		if err != nil {
			continue
		}
		if version > inventory.highest {
			inventory.highest = version
		}
	}
	return inventory
}

// migrationName returns the name part of a migration file name — the version
// prefix and the extension stripped — normalized the same way as a name typed
// on the command line, so "Add-Widgets.sql" and "add_widgets" describe the same
// migration.
//
// Only a numeric prefix is stripped. A file without one, such as a helper
// script, keeps its whole name instead of losing the part before its first
// underscore.
func migrationName(fileName string) string {
	base := strings.TrimSuffix(fileName, filepath.Ext(fileName))

	if before, rest, found := strings.Cut(base, "_"); found {
		if version, err := strconv.ParseInt(before, 10, 64); err == nil && version >= 1 {
			base = rest
		}
	}
	return slugify(base)
}

// slugify turns a typed name into a file name fragment: lowercase ASCII
// letters and digits, with every other character collapsed into one
// underscore. A run of them becomes a single separator, so "Add  Widgets"
// and "add-widgets" both become "add_widgets".
func slugify(name string) string {
	var (
		slug    strings.Builder
		pending bool
	)
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pending && slug.Len() > 0 {
				slug.WriteByte('_')
			}
			pending = false
			slug.WriteRune(r)
			continue
		}
		pending = true
	}
	return slug.String()
}
