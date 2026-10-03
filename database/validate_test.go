package database_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/database"
)

// validMigration is the shape every file in database/migrations must have.
const validMigration = `-- +goose Up
-- +goose StatementBegin
CREATE TABLE example (id UUID NOT NULL PRIMARY KEY);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE example;
-- +goose StatementEnd
`

// The migrations shipped in the binary must pass, otherwise migrate:validate
// would fail on a clean checkout.
func TestValidateEmbeddedMigrations(t *testing.T) {
	report := database.Validate()

	assert.True(t, report.OK(), "issues: %v", report.Issues)
	assert.Equal(t, migrationCount(), report.Checked)
}

func TestValidateAcceptsValidMigrations(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00001_initialize_schema.sql":      validMigration,
		"00002_create_identity_tables.sql": validMigration,
	})

	assert.True(t, report.OK(), "issues: %v", report.Issues)
	assert.Equal(t, 2, report.Checked)
}

// goose silently skips a file it cannot parse, so the validator must not.
func TestValidateRejectsUnparsableFileName(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00001_initialize_schema.sql": validMigration,
		"bootstrap.sql":               validMigration,
	})

	require.False(t, report.OK())
	assert.Contains(t, issuesText(report), "bootstrap.sql: goose will skip this file")
	// The unparsable file is not counted as checked, because goose would not run it.
	assert.Equal(t, 1, report.Checked)
}

func TestValidateRejectsZeroVersion(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00000_initialize_schema.sql": validMigration,
	})

	require.False(t, report.OK())
	assert.Contains(t, issuesText(report), "goose will skip this file")
}

func TestValidateRejectsShortVersionPrefix(t *testing.T) {
	report := validateFS(t, map[string]string{
		"1_initialize_schema.sql": validMigration,
	})

	require.False(t, report.OK())
	assert.Contains(t, issuesText(report), "version prefix must be 5 digits")
}

func TestValidateRejectsDuplicateVersion(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00001_initialize_schema.sql": validMigration,
		"00001_other_name.sql":        validMigration,
	})

	require.False(t, report.OK())
	assert.Contains(t, issuesText(report), "duplicate version 1")
}

func TestValidateRejectsGapInVersions(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00001_initialize_schema.sql": validMigration,
		"00003_create_identity.sql":   validMigration,
	})

	require.False(t, report.OK())
	assert.Contains(t, issuesText(report), "versions must be consecutive from 1")
	// One gap explains every later version, so it is reported once.
	assert.Equal(t, 1, countIssues(report, "versions must be consecutive"))
}

func TestValidateRejectsMissingUp(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00001_initialize_schema.sql": "-- +goose Down\nDROP TABLE example;\n",
	})

	require.False(t, report.OK())
	assert.Contains(t, issuesText(report), "missing '-- +goose Up' annotation")
}

// A file without Down can be applied but never rolled back, which this project
// forbids.
func TestValidateRejectsMissingDown(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00001_initialize_schema.sql": "-- +goose Up\nCREATE TABLE example (id int);\n",
	})

	require.False(t, report.OK())
	assert.Contains(t, issuesText(report), "cannot be rolled back")
}

func TestValidateRejectsDuplicateUp(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00001_initialize_schema.sql": "-- +goose Up\nSELECT 1;\n-- +goose Up\nSELECT 2;\n-- +goose Down\nSELECT 3;\n",
	})

	require.False(t, report.OK())
	assert.Contains(t, issuesText(report), "duplicate '-- +goose Up' annotation")
}

func TestValidateRejectsDownBeforeUp(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00001_initialize_schema.sql": "-- +goose Down\nSELECT 1;\n-- +goose Up\nSELECT 2;\n",
	})

	require.False(t, report.OK())
	assert.Contains(t, issuesText(report), "'-- +goose Down' before '-- +goose Up'")
}

func TestValidateRejectsUnbalancedStatement(t *testing.T) {
	tests := map[string]struct {
		content string
		want    string
	}{
		"unterminated": {
			content: "-- +goose Up\n-- +goose StatementBegin\nSELECT 1;\n-- +goose Down\nSELECT 2;\n",
			want:    "missing '-- +goose StatementEnd'",
		},
		"end without begin": {
			content: "-- +goose Up\nSELECT 1;\n-- +goose StatementEnd\n-- +goose Down\nSELECT 2;\n",
			want:    "'-- +goose StatementEnd' without '-- +goose StatementBegin'",
		},
		"begin outside a block": {
			content: "-- +goose StatementBegin\nSELECT 1;\n-- +goose StatementEnd\n-- +goose Up\nSELECT 2;\n-- +goose Down\nSELECT 3;\n",
			want:    "outside an Up or Down block",
		},
		"nested begin": {
			content: "-- +goose Up\n-- +goose StatementBegin\n-- +goose StatementBegin\nSELECT 1;\n-- +goose StatementEnd\n-- +goose Down\nSELECT 2;\n",
			want:    "inside another statement",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			report := validateFS(t, map[string]string{"00001_x.sql": tt.content})

			require.False(t, report.OK())
			assert.Contains(t, issuesText(report), tt.want)
		})
	}
}

func TestValidateRejectsUnknownAnnotation(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00001_x.sql": "-- +goose Up\n-- +goose StatementBegins\nSELECT 1;\n-- +goose Down\nSELECT 2;\n",
	})

	require.False(t, report.OK())
	assert.Contains(t, issuesText(report), `unknown annotation "statementbegins"`)
}

func TestValidateRejectsIndentedAnnotation(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00001_x.sql": "-- +goose Up\n  -- +goose StatementBegin\nSELECT 1;\n  -- +goose StatementEnd\n-- +goose Down\nSELECT 2;\n",
	})

	require.False(t, report.OK())
	assert.Contains(t, issuesText(report), "annotation must not be indented")
}

func TestValidateRejectsStatementBeforeUp(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00001_x.sql": "SELECT 1;\n-- +goose Up\nSELECT 2;\n-- +goose Down\nSELECT 3;\n",
	})

	require.False(t, report.OK())
	assert.Contains(t, issuesText(report), "statement before '-- +goose Up'")
}

// Prose that mentions goose is not a directive.
func TestValidateIgnoresProseAboutGoose(t *testing.T) {
	report := validateFS(t, map[string]string{
		"00001_x.sql": "-- +goose Up\n-- see the +goose docs for the syntax\nSELECT 1;\n-- +goose Down\nSELECT 2;\n",
	})

	assert.True(t, report.OK(), "issues: %v", report.Issues)
}

// The message must carry the file and line so the fix is obvious.
func TestValidationIssueString(t *testing.T) {
	tests := map[string]struct {
		issue database.ValidationIssue
		want  string
	}{
		"file and line": {issue: database.ValidationIssue{File: "00001_x.sql", Line: 3, Message: "boom"}, want: "00001_x.sql:3: boom"},
		"file only":     {issue: database.ValidationIssue{File: "00001_x.sql", Message: "boom"}, want: "00001_x.sql: boom"},
		"neither":       {issue: database.ValidationIssue{Message: "boom"}, want: "boom"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.issue.String())
		})
	}
}

// validateFS runs the validator over in-memory files.
func validateFS(t *testing.T, files map[string]string) database.ValidationReport {
	t.Helper()

	fsys := fstest.MapFS{}
	for name, content := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return database.ValidateFS(fsys)
}

func issuesText(report database.ValidationReport) string {
	text := ""
	for _, issue := range report.Issues {
		text += issue.String() + "\n"
	}
	return text
}

func countIssues(report database.ValidationReport, fragment string) int {
	count := 0
	for _, issue := range report.Issues {
		if strings.Contains(issue.Message, fragment) {
			count++
		}
	}
	return count
}
