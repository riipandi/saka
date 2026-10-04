//go:build debug

package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/internal/database"
	"github.com/riipandi/saka/pkg/printext"
)

// plain is the palette a test uses. A bytes.Buffer is never a terminal, so the
// palette adds no colour and the expected strings stay readable.
func plain(w *bytes.Buffer) printext.Palette { return printext.NewPalette(w) }

// A summary is a labelled block, so the values line up in a column and a reader
// scans labels instead of parsing a sentence.
func TestPrintFieldsAlignsValues(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, printFields(plain(&out), []field{
		{label: "written", value: "/tmp/dump.sql"},
		{label: "schema", value: "324 statements"},
		{label: "duration", value: "84 ms"},
	}))

	assert.Equal(t, "written:   /tmp/dump.sql\n"+
		"schema:    324 statements\n"+
		"duration:  84 ms\n", out.String())
}

// The export summary reports what was written and where, and omits a section it
// did not produce rather than printing a zero.
func TestPrintExportSummary(t *testing.T) {
	var out bytes.Buffer
	err := printExportSummary(plain(&out), "/tmp/dump.sql", database.DumpStats{
		Statements: 12,
		Tables:     3,
		Rows:       7,
	}, 1500*time.Millisecond)
	require.NoError(t, err)

	assert.Contains(t, out.String(), "written:   /tmp/dump.sql")
	assert.Contains(t, out.String(), "schema:    12 statements")
	assert.Contains(t, out.String(), "data:      3 tables, 7 rows")
	assert.Contains(t, out.String(), "duration:  1.5 s")
}

// A data-only dump has no schema section, and a schema-only dump has no data
// section. Neither may report a zero.
func TestPrintExportSummaryOmitsAbsentSections(t *testing.T) {
	var dataOnly bytes.Buffer
	require.NoError(t, printExportSummary(plain(&dataOnly), "dump.sql",
		database.DumpStats{Tables: 1, Rows: 2}, time.Second))
	assert.NotContains(t, dataOnly.String(), "schema:")
	assert.Contains(t, dataOnly.String(), "data:")

	var schemaOnly bytes.Buffer
	require.NoError(t, printExportSummary(plain(&schemaOnly), "dump.sql",
		database.DumpStats{Statements: 5}, time.Second))
	assert.Contains(t, schemaOnly.String(), "schema:")
	assert.NotContains(t, schemaOnly.String(), "data:")
}

// The import summary names the file it read.
func TestPrintImportSummary(t *testing.T) {
	var out bytes.Buffer
	err := printImportSummary(plain(&out), "dump.sql",
		database.DumpStats{Tables: 40, Rows: 2}, 20*time.Millisecond)
	require.NoError(t, err)

	assert.Contains(t, out.String(), "loaded:    dump.sql")
	assert.Contains(t, out.String(), "data:      40 tables, 2 rows")
	assert.Contains(t, out.String(), "duration:  20 ms")
}

// The spinner only draws on a terminal, so a redirected run writes nothing and
// the report stays the only output.
func TestSpinnerStaysSilentOffTerminal(t *testing.T) {
	var out bytes.Buffer
	progress := withSpinner(&out)

	progress.step(database.TableProgress{Table: database.Table{Schema: "public", Name: "users"}})
	progress.finish()

	assert.Empty(t, out.String())
}

// The target opens the report block and the result closes it. Both calls pad
// their labels to the same width, so the block stays aligned across the two
// writes and the credentials never appear.
func TestPrintDatabaseAlignsWithTheSummary(t *testing.T) {
	var out bytes.Buffer
	p := plain(&out)
	require.NoError(t, printDatabase(p, "postgres://user:secret@db.example.com:5432/app"))
	require.NoError(t, printExportSummary(p, "dump.sql", database.DumpStats{
		Statements: 3,
	}, time.Second))

	assert.Equal(t, "database:  db.example.com:5432/app\n"+
		"written:   dump.sql\n"+
		"schema:    3 statements\n"+
		"duration:  1 s\n", out.String())
	assert.NotContains(t, out.String(), "secret")
}

// A DSN that cannot be parsed still prints a target line, with the value
// replaced rather than echoed: an unparsable DSN may carry a password in a form
// the parser did not recognise, and a report must never print it.
func TestPrintDatabaseRedactsUnparsableDSN(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, printDatabase(plain(&out), "not a dsn"))

	assert.Contains(t, out.String(), "[redacted]")
	assert.NotContains(t, out.String(), "not a dsn")
}

// The target line names the database without its credentials, so a report can
// be pasted into a ticket.
func TestReportTargetOmitsCredentials(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, reportTarget(plain(&out),
		"postgres://user:secret@db.example.com:5432/saka?sslmode=disable"))

	assert.Equal(t, "database: db.example.com:5432/saka\n\n", out.String())
	assert.NotContains(t, out.String(), "secret")
}

// A DSN that cannot be parsed must not stop the command, and its value must not
// reach the report.
func TestReportTargetRedactsUnparsableDSN(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, reportTarget(plain(&out), "not a dsn"))

	assert.Contains(t, out.String(), "[redacted]")
	assert.NotContains(t, out.String(), "not a dsn")
}

// Progress lines are indented and summaries are not, so a summary can be grepped
// at the start of a line.
func TestReporterIndentsProgressOnly(t *testing.T) {
	var out bytes.Buffer
	p := plain(&out)
	report := newReporter(p, migrationStateWidth(string(database.ProgressApplied)))

	report.progress(database.ProgressEvent{
		Version:   2,
		Name:      "00002_create_identity_tables.sql",
		Direction: "up",
		State:     database.ProgressApplied,
		Duration:  37 * time.Millisecond,
	})
	require.NoError(t, report.failed())

	// A finished migration is reported in the same shape migrate:status uses:
	// version, state, time, name, and duration.
	assert.Regexp(t,
		`^  00002 applied \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} create_identity_tables \(37 ms\)\n$`,
		out.String())

	out.Reset()
	require.NoError(t, printSummary(p, 1, "applied", "migration", 100*time.Millisecond))

	line := strings.TrimSuffix(out.String(), "\n")
	line = strings.TrimPrefix(line, "\n")
	assert.True(t, strings.HasPrefix(line, "status: 1 migration applied in "), "summary must start at column zero: %q", line)
}

// A rollback must be reported the same way an apply is, with its own state word
// and duration, so the two directions read as one report.
func TestReporterReportsRollbackRows(t *testing.T) {
	var out bytes.Buffer
	report := newReporter(plain(&out), migrationStateWidth(string(database.ProgressRolledBack)))

	report.progress(database.ProgressEvent{
		Version:   9,
		Name:      "00009_add_session_remember.sql",
		Direction: "down",
		State:     database.ProgressRolledBack,
		Duration:  3 * time.Millisecond,
	})
	require.NoError(t, report.failed())

	assert.Regexp(t,
		`^  00009 rolled back \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} add_session_remember \(3 ms\)\n$`,
		out.String())
}

// An empty migration is a state to notice, not a failure, and it still carries
// its duration because it did run.
func TestReporterReportsEmptyMigration(t *testing.T) {
	var out bytes.Buffer
	report := newReporter(plain(&out), migrationStateWidth(string(database.ProgressApplied)))

	report.progress(database.ProgressEvent{
		Version:   10,
		Name:      "00010_noop.sql",
		Direction: "up",
		State:     database.ProgressEmpty,
		Duration:  0,
	})
	require.NoError(t, report.failed())

	assert.Contains(t, out.String(), "  00010 empty ")
	assert.Contains(t, out.String(), "noop (0 s)")
}

// A dry run lists work that has not happened, so the state column reads
// "pending" and the time column is empty rather than a fabricated timestamp.
func TestPrintPendingUsesTheSharedRowShape(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, printPending(plain(&out), []database.MigrationStatus{
		{Version: 1, Name: "00001_initialize_schema.sql"},
		{Version: 2, Name: "00002_create_identity_tables.sql"},
	}))

	assert.Equal(t,
		expectedRow(1, "pending", 7, "-", "initialize_schema")+
			expectedRow(2, "pending", 7, "-", "create_identity_tables")+
			"\nstatus: 2 migrations pending\n", out.String())
}

// A rollback plan says "rollback", not "rolled back": nothing has run yet.
func TestPrintRollbackUsesTheSharedRowShape(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, printRollback(plain(&out), []database.MigrationStatus{
		{Version: 9, Name: "00009_add_session_remember.sql"},
	}))

	assert.Equal(t,
		expectedRow(9, "rollback", 8, "-", "add_session_remember")+
			"\nstatus: 1 migration to roll back\n", out.String())
}

// Every outcome line carries the same "status:" label at column zero, so one
// grep finds the result of any migrate:* command instead of one pattern per
// command.
func TestPrintSummaryLabelsEveryOutcome(t *testing.T) {
	tests := []struct {
		name string
		verb string
	}{
		{name: "applied", verb: "applied"},
		{name: "rolled back", verb: "rolled back"},
		{name: "pending", verb: "pending"},
		{name: "to roll back", verb: "to roll back"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, printSummary(plain(&out), 3, tt.verb, "migration", 5*time.Millisecond))

			line := strings.TrimPrefix(out.String(), "\n")
			assert.True(t, strings.HasPrefix(line, "status: "), "summary must carry the label: %q", line)
			assert.Contains(t, line, tt.verb)
			assert.Contains(t, line, "in 5 ms")
		})
	}
}

// A report that measured nothing still labels its outcome, so the label is on
// every result and not only the timed ones.
func TestPrintSummaryLabelsAnUntimedOutcome(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, printSummary(plain(&out), 1, "pending", "migration", 0))

	assert.Equal(t, "\nstatus: 1 migration pending\n", out.String())
}

// The lines that report "nothing happened" are outcomes too, so they carry the
// same label.
func TestPrintStatusLineCarriesTheLabel(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, printStatusLine(plain(&out), "no pending migrations"))

	assert.Equal(t, "status: no pending migrations\n", out.String())
}

// The label is formatted like the field labels, so a reader cannot tell an
// outcome line from the rest of the block.
func TestPrintStatusLineFormatsItsArguments(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, printStatusLine(plain(&out), "%d %s left applied", 2, "migrations"))

	assert.Equal(t, "status: 2 migrations left applied\n", out.String())
}

// expectedRow builds one row of the shared report shape, with the widths written
// out rather than counted by hand, so a test cannot drift from the renderer by a
// single space.
func expectedRow(version int64, state string, stateWidth int, at, name string) string {
	return fmt.Sprintf("  %05d %-*s %-*s %s\n",
		version, stateWidth, state, migrationTimestampWidth, at, name)
}

// A row names a migration by its short label: the version has its own column and
// ".sql" says nothing. The full file name is still what the row is built from.
func TestMigrationLabel(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "00009_add_session_remember.sql", want: "add_session_remember"},
		{name: "00001_initialize_schema.sql", want: "initialize_schema"},
		{name: "add_widgets.sql", want: "add_widgets"},
		{name: "no_extension", want: "no_extension"},
		{name: "00010.sql", want: "00010"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, migrationLabel(tt.name))
		})
	}
}

// A started event starts the spinner and draws no line of its own: a line for a
// migration that is still running would be overwritten on a terminal and lost in
// a log.
func TestReporterIgnoresStartedEvents(t *testing.T) {
	var out bytes.Buffer
	report := newReporter(plain(&out), migrationStateWidth(string(database.ProgressApplied)))

	report.progress(database.ProgressEvent{
		Version:   1,
		Name:      "00001_initialize_schema.sql",
		Direction: "up",
		State:     database.ProgressStarted,
	})

	require.NoError(t, report.failed())
	assert.Empty(t, out.String(), "a redirected run must not draw the spinner")
}

// A write failure cannot be returned from the progress callback, so it must be
// held and surfaced when the command can report it.
func TestReporterHoldsWriteFailure(t *testing.T) {
	report := newReporter(printext.NewPalette(failingWriter{}), migrationStateWidth(string(database.ProgressApplied)))

	report.progress(database.ProgressEvent{
		Version:   1,
		Name:      "00001_initialize_schema.sql",
		Direction: "up",
		State:     database.ProgressApplied,
		Duration:  time.Millisecond,
	})

	require.ErrorIs(t, report.failed(), assert.AnError)
}

// failingWriter rejects every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, assert.AnError }
