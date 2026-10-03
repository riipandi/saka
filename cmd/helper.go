package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/briandowns/spinner"

	"github.com/riipandi/saka/database"
	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/pkg/printext"
)

// spinnerDelay is how fast the indicator animates. The braille set has ten
// frames, so a whole turn takes about a second: fast enough to read as activity,
// slow enough not to flicker.
const spinnerDelay = 100 * time.Millisecond

// spinnerChars is the braille set. It is one column wide and never changes
// width, so the suffix beside it cannot shift.
var spinnerChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// newSpinner returns a spinner bound to w, or nil when w is not a terminal.
//
// The animation redraws its line with carriage returns, so it is only drawn
// where a line can be redrawn: redirected to a file or a pipe it would fill the
// log with control codes. A nil spinner draws nothing, which is what every
// method below checks for.
func newSpinner(w io.Writer) *spinner.Spinner {
	if !printext.IsTerminal(w) {
		return nil
	}
	return spinner.New(spinnerChars, spinnerDelay,
		spinner.WithWriter(w),
		spinner.WithHiddenCursor(true),
		// Only the glyph is coloured. The suffix stays plain because the spinner
		// measures its own output to erase it, and a colour code would be
		// counted as visible text.
		spinner.WithColor("yellow"),
	)
}

// tableSpinner shows that a dump or a restore is running, and which table it is
// on. A spinner is used rather than a progress bar because the number of rows is
// not known before a table has been read: a bar would have to be sized from a
// second query, and it would still stall for the whole of a large table. What a
// reader needs here is proof the command is alive and the name of the work in
// flight.
type tableSpinner struct {
	spinner *spinner.Spinner
}

// withSpinner returns the spinner a dump or a restore should use.
func withSpinner(w io.Writer) *tableSpinner {
	return &tableSpinner{spinner: newSpinner(w)}
}

// step names the table now in flight. It starts the spinner on the first call,
// because the table is the only progress worth reporting and it is not known
// until the work starts.
func (t *tableSpinner) step(progress database.TableProgress) {
	if t == nil || t.spinner == nil {
		return
	}
	t.spinner.Suffix = " " + progress.Table.String()
	if !t.spinner.Active() {
		t.spinner.Start()
	}
}

// finish stops the spinner. Stopping erases the line and leaves the cursor at
// column zero, so the report that follows overwrites it instead of leaving a
// blank line behind. When the spinner never ran nothing was drawn and the blank
// line above the report is already the only separation.
func (t *tableSpinner) finish() {
	if t == nil || t.spinner == nil {
		return
	}
	t.spinner.Stop()
}

// progressIndent marks a line that reports one step inside a run, so a reader
// can tell progress from the summary that closes it.
const progressIndent = "  "

// reportTarget announces the database a command works on, followed by a blank
// line. It is used where progress lines follow, so the target stays separate
// from the run beneath it.
func reportTarget(p printext.Palette, dsn string) error {
	target := config.RedactDSN(dsn)
	if target == "" {
		return nil
	}
	return p.Printf("%s %s\n\n", p.Dim("database:"), p.Dim(target))
}

// migrationRow is one line of a migration report. Every migrate:* command
// builds these, so a migration reads the same whether it is listed, applied, or
// rolled back.
type migrationRow struct {
	Version int64
	// State is the word in the state column: "applied", "empty", "rolled back",
	// or "pending".
	State string
	// At is when the migration last ran. The zero value means it has not run,
	// which is rendered as "-".
	At   time.Time
	Name string
	// Duration is how long the migration took, and Measured says whether there
	// is one to show. A measured zero is a real answer (a migration with no
	// statements takes no time), so the two cannot be one field.
	Duration time.Duration
	Measured bool
}

// The state words a report uses for a migration that has not run, and for the
// plan a dry run describes. The words for a migration that has run come from
// database.ProgressState.
const (
	statePending  = "pending"
	stateRollback = "rollback"
)

// migrationStateWidth is the width the state column needs for the given words.
//
// It is computed per report rather than fixed, so a run that only applies
// ("applied") is not padded to the width of one that rolls back ("rolled back"),
// while two lists printed by one command still share a column.
func migrationStateWidth(states ...string) int {
	width := 0
	for _, state := range states {
		width = max(width, len(state))
	}
	return width
}

// migrationFileExt is the extension a migration file carries.
const migrationFileExt = ".sql"

// migrationLabel shortens a migration file name for a report row.
//
// The version already has its own column and the ".sql" says nothing a reader
// does not know, so both are dropped: "00009_add_session_remember.sql" reads as
// "add_session_remember". The prefix is only removed when it really is a version,
// so a file named "add_widgets.sql" keeps its name.
func migrationLabel(name string) string {
	label := strings.TrimSuffix(name, migrationFileExt)
	prefix, rest, ok := strings.Cut(label, "_")
	if ok && isDigits(prefix) {
		return rest
	}
	return label
}

// isDigits reports whether s is non-empty and holds only ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// printMigrationRows renders one line per migration with the columns every
// migrate:* command shares: version, state, time, name, and duration. One
// renderer is what makes `migrate:up`, `migrate:down`, and `migrate:status` read
// as the same report instead of three dialects of it.
func printMigrationRows(p printext.Palette, stateWidth int, rows []migrationRow) error {
	for _, row := range rows {
		at := "-"
		if !row.At.IsZero() {
			at = row.At.UTC().Format(migrationTimestamp)
		}

		// Pad before colouring, never after: an escape code is invisible but
		// not zero-width to fmt, so padding a painted string would break the
		// column it was meant to hold.
		line := fmt.Sprintf("%s%05d %s %s %s",
			progressIndent,
			row.Version,
			p.Paint(stateColour(row.State), printext.PadRight(row.State, stateWidth)),
			p.Dim(printext.PadRight(at, migrationTimestampWidth)),
			migrationLabel(row.Name))
		if row.Measured {
			line += " " + p.Dim("("+printext.Duration(row.Duration)+")")
		}
		if err := p.Printf("%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

// stateColour maps a migration state to its colour. Applied is a success; an
// empty migration and a rollback both did what was asked, so they are states to
// notice rather than failures.
func stateColour(state string) printext.Colour {
	if state == string(database.ProgressApplied) {
		return printext.Green
	}
	return printext.Yellow
}

// reporter renders a migration run while it happens.
//
// goose reports each migration through the Progress callback, so the run is
// visible as it happens rather than appearing all at once at the end. A
// migration that is still running is shown by the spinner, because a line for it
// would be overwritten on a terminal and lost in a log; a migration that has
// finished becomes a line that stays, in the same shape migrate:status prints.
type reporter struct {
	p       printext.Palette
	started time.Time
	spin    *spinner.Spinner
	// stateWidth is the width of the state column, which the caller knows from
	// the direction of the run: an apply reports "applied", a rollback reports
	// "rolled back".
	stateWidth int
	// err is the first write failure. A progress callback cannot return one, so
	// it is held until the command can report it.
	err error
}

// newReporter starts timing a run. stateWidth is the width of the state column,
// from migrationStateWidth.
func newReporter(p printext.Palette, stateWidth int) *reporter {
	return &reporter{
		p:          p,
		started:    time.Now(),
		spin:       newSpinner(p.Writer()),
		stateWidth: stateWidth,
	}
}

// progress is the callback handed to the migrator.
func (r *reporter) progress(event database.ProgressEvent) {
	if r.err != nil {
		return
	}
	if event.State == database.ProgressStarted {
		r.begin(event)
		return
	}
	r.stop()
	r.err = printMigrationRows(r.p, r.stateWidth, []migrationRow{{
		Version: event.Version,
		State:   string(event.State),
		// The migration has just finished, so now is when it ran. Reading the
		// recorded time back would give the same answer for an apply and nothing
		// at all for a rollback, whose row goose deletes.
		At:       time.Now(),
		Name:     event.Name,
		Duration: event.Duration,
		Measured: true,
	}})
}

// begin names the migration now in flight and starts the animation.
func (r *reporter) begin(event database.ProgressEvent) {
	if r.spin == nil {
		return
	}
	r.spin.Suffix = " " + event.Name
	if !r.spin.Active() {
		r.spin.Start()
	}
}

// stop ends the animation. Stopping erases the line and leaves the cursor at
// column zero, so the next line written lands where the spinner was.
func (r *reporter) stop() {
	if r.spin != nil {
		r.spin.Stop()
	}
}

// failed reports a write failure that happened while the run was in progress,
// and clears the spinner first so a failed run does not leave a hidden cursor.
func (r *reporter) failed() error {
	r.stop()
	return r.err
}

// elapsed is how long the command has been running.
func (r *reporter) elapsed() time.Duration { return time.Since(r.started) }

// statusLabel is the prefix on every outcome line a migration command prints, so
// the result of any of them greps at column zero with one pattern:
//
//	grep '^status:'
func statusLabel(p printext.Palette) string { return p.Dim("status:") }

// printStatusLine writes one outcome line: a labelled statement of what the
// command did, or did not do.
func printStatusLine(p printext.Palette, format string, args ...any) error {
	return p.Printf("%s %s\n", statusLabel(p), fmt.Sprintf(format, args...))
}

// printSummary closes a report with what happened and how long it took.
//
// The line carries the same "status:" label as every other outcome line, so the
// result of a run greps at column zero. The duration is omitted when nothing was
// measured.
func printSummary(p printext.Palette, count int, verb, noun string, elapsed time.Duration) error {
	head := fmt.Sprintf("%d %s ", count, printext.Plural(count, noun))
	if elapsed > 0 {
		return p.Printf("\n%s %s%s %s\n", statusLabel(p), head, p.Paint(verbAttribute(verb), verb),
			p.Dim("in "+printext.Duration(elapsed)))
	}
	return p.Printf("\n%s %s%s\n", statusLabel(p), head, p.Paint(verbAttribute(verb), verb))
}

// verbAttribute maps the verb that closes a report to its colour: work that
// landed is a success, and anything else is a state to notice.
func verbAttribute(verb string) printext.Colour {
	if verb == "applied" {
		return printext.Green
	}
	return printext.Yellow
}
