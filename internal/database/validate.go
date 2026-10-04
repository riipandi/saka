package database

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/pressly/goose/v3"
)

// ValidationIssue is one problem found in the migration files.
type ValidationIssue struct {
	// File is the file name, empty when the problem is about the set of files.
	File string
	// Line is the 1-based line number, zero when the problem is not tied to one.
	Line    int
	Message string
}

// String renders the issue with as much location as it has.
func (i ValidationIssue) String() string {
	switch {
	case i.File == "":
		return i.Message
	case i.Line == 0:
		return fmt.Sprintf("%s: %s", i.File, i.Message)
	default:
		return fmt.Sprintf("%s:%d: %s", i.File, i.Line, i.Message)
	}
}

// ValidationReport is the outcome of Validate.
type ValidationReport struct {
	// Checked is the number of migration files examined.
	Checked int
	Issues  []ValidationIssue
}

// OK reports whether the migrations passed every check.
func (r ValidationReport) OK() bool { return len(r.Issues) == 0 }

// EmbeddedMigration is one migration compiled into this binary.
type EmbeddedMigration struct {
	// Name is the full file name, version prefix included.
	Name string
	// Version is the numeric prefix of the file name.
	Version int64
	// SQL is the file's content, Up and Down blocks included.
	SQL string
}

// EmbeddedMigrations returns the migrations compiled into this binary, in
// version order. Tests and tooling derive their expectations from this list
// instead of hardcoding counts that go stale with every new file.
func EmbeddedMigrations() ([]EmbeddedMigration, error) {
	fsys, err := fs.Sub(migrationFiles, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("database: open embedded migrations: %w", err)
	}

	files, issues := listMigrations(fsys)
	if len(issues) > 0 {
		messages := make([]string, len(issues))
		for i, issue := range issues {
			messages[i] = issue.String()
		}
		return nil, fmt.Errorf("database: embedded migrations do not validate: %s", strings.Join(messages, "; "))
	}

	out := make([]EmbeddedMigration, 0, len(files))
	for _, file := range files {
		sql, err := fs.ReadFile(fsys, file.name)
		if err != nil {
			return nil, fmt.Errorf("database: read %s: %w", file.name, err)
		}
		out = append(out, EmbeddedMigration{Name: file.name, Version: file.version, SQL: string(sql)})
	}
	return out, nil
}

// Validate checks the embedded migrations for the structural mistakes goose
// rejects while applying. It reads nothing outside the binary and needs no
// database, so it runs in CI before a connection exists.
//
// It does not parse SQL. A malformed statement, a missing semicolon inside a
// StatementBegin block, or a typo in a table name is only caught when goose
// executes the migration.
func Validate() ValidationReport {
	fsys, err := fs.Sub(migrationFiles, migrationsDir)
	if err != nil {
		return ValidationReport{Issues: []ValidationIssue{
			{Message: fmt.Sprintf("open embedded migrations: %v", err)},
		}}
	}
	return ValidateFS(fsys)
}

// ValidateFS is Validate over any filesystem, so the checks can be exercised
// with fixtures instead of only the files this build embedded.
func ValidateFS(fsys fs.FS) ValidationReport {
	files, issues := listMigrations(fsys)
	issues = append(issues, validateVersions(files)...)

	for _, file := range files {
		issues = append(issues, validateMigrationFile(fsys, file.name)...)
	}
	return ValidationReport{Checked: len(files), Issues: issues}
}

// MigrationPrefixWidth is the number of digits a migration file name reserves
// for its version, so a directory listing and the numeric order agree. It lives
// here because validate, create, and the commands all report a version.
const MigrationPrefixWidth = 5

// embeddedMigration is one file in the migrations directory.
type embeddedMigration struct {
	name    string
	version int64
}

// listMigrations reads the directory and parses each file name. A name that
// goose cannot parse is reported and skipped, because goose would skip it too —
// silently, which is the trap this check exists for.
func listMigrations(fsys fs.FS) ([]embeddedMigration, []ValidationIssue) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, []ValidationIssue{{Message: fmt.Sprintf("read migrations: %v", err)}}
	}

	var (
		files  []embeddedMigration
		issues []ValidationIssue
	)
	for _, entry := range entries {
		name := entry.Name()

		version, err := goose.NumericComponent(name)
		if err != nil {
			issues = append(issues, ValidationIssue{File: name,
				Message: fmt.Sprintf("goose will skip this file: %v", err)})
			continue
		}

		if prefix, _, _ := strings.Cut(name, "_"); len(prefix) != MigrationPrefixWidth {
			issues = append(issues, ValidationIssue{File: name, Message: fmt.Sprintf(
				"version prefix must be %d digits, found %q", MigrationPrefixWidth, prefix)})
		}
		files = append(files, embeddedMigration{name: name, version: version})
	}

	slices.SortFunc(files, func(a, b embeddedMigration) int { return int(a.version - b.version) })
	return files, issues
}

// validateVersions reports duplicate versions and gaps in the sequence. goose
// tolerates both; this project does not, because the file order and the applied
// order must match.
func validateVersions(files []embeddedMigration) []ValidationIssue {
	var issues []ValidationIssue

	byVersion := make(map[int64]string, len(files))
	for _, file := range files {
		if other, ok := byVersion[file.version]; ok {
			issues = append(issues, ValidationIssue{File: file.name, Message: fmt.Sprintf(
				"duplicate version %d, already used by %s", file.version, other)})
			continue
		}
		byVersion[file.version] = file.name
	}

	for i, version := range slices.Sorted(maps.Keys(byVersion)) {
		if want := int64(i + 1); version != want {
			issues = append(issues, ValidationIssue{Message: fmt.Sprintf(
				"versions must be consecutive from 1: expected %0*d, found %0*d (%s)",
				MigrationPrefixWidth, want, MigrationPrefixWidth, version, byVersion[version])})
			// One gap explains every later version, so report it once.
			break
		}
	}
	return issues
}

// gooseAnnotation marks a goose directive line.
const gooseAnnotation = "+goose"

// knownAnnotations are the directives goose accepts, lowercased.
var knownAnnotations = []string{
	"up",
	"down",
	"statementbegin",
	"statementend",
	"no transaction",
	"envsub on",
	"envsub off",
}

// validateMigrationFile walks one file and reports the annotation mistakes
// goose would reject, plus the two shapes it accepts but this project forbids:
// a file without a Down block, and a statement outside Up or Down.
func validateMigrationFile(fsys fs.FS, name string) []ValidationIssue {
	content, err := fs.ReadFile(fsys, name)
	if err != nil {
		return []ValidationIssue{{File: name, Message: fmt.Sprintf("read file: %v", err)}}
	}

	var (
		issues      []ValidationIssue
		upSeen      bool
		downSeen    bool
		section     string
		inStatement bool
	)

	report := func(line int, message string) {
		issues = append(issues, ValidationIssue{File: name, Line: line, Message: message})
	}

	lines := strings.Split(string(content), "\n")
	for i, raw := range lines {
		line := i + 1
		trimmed := strings.TrimSpace(raw)

		if strings.Contains(trimmed, gooseAnnotation) {
			directive, isAnnotation := parseAnnotation(trimmed)
			if !isAnnotation {
				continue
			}
			if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
				report(line, "annotation must not be indented")
			}
			if strings.Count(trimmed, gooseAnnotation) > 1 {
				report(line, "line carries more than one goose annotation")
			}
			if !slices.Contains(knownAnnotations, directive) {
				report(line, fmt.Sprintf("unknown annotation %q", directive))
				continue
			}

			switch directive {
			case "up":
				if upSeen {
					report(line, "duplicate '-- +goose Up' annotation")
				}
				upSeen = true
				section = "up"
			case "down":
				if !upSeen {
					report(line, "'-- +goose Down' before '-- +goose Up'")
				}
				if downSeen {
					report(line, "duplicate '-- +goose Down' annotation")
				}
				downSeen = true
				section = "down"
			case "statementbegin":
				switch {
				case section == "":
					report(line, "'-- +goose StatementBegin' outside an Up or Down block")
				case inStatement:
					report(line, "'-- +goose StatementBegin' inside another statement")
				}
				inStatement = true
			case "statementend":
				if !inStatement {
					report(line, "'-- +goose StatementEnd' without '-- +goose StatementBegin'")
				}
				inStatement = false
			}
			continue
		}

		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		if section == "" {
			report(line, "statement before '-- +goose Up'")
		}
	}

	switch {
	case inStatement:
		report(len(lines), "missing '-- +goose StatementEnd'")
	case !upSeen:
		report(0, "missing '-- +goose Up' annotation")
	case !downSeen:
		report(0, "missing '-- +goose Down' annotation, so the migration cannot be rolled back")
	}
	return issues
}

// parseAnnotation reads a "-- +goose <directive>" line and returns the
// directive, lowercased. isAnnotation is false for a line that only mentions
// goose in a comment, so prose about goose is not mistaken for a directive.
func parseAnnotation(line string) (directive string, isAnnotation bool) {
	if !strings.HasPrefix(line, "--") {
		return "", false
	}
	rest, found := strings.CutPrefix(line, "--")
	if !found {
		return "", false
	}
	rest, found = strings.CutPrefix(strings.TrimSpace(rest), gooseAnnotation)
	if !found {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(rest)), true
}
