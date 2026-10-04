package database

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riipandi/saka/internal/database/entity"
)

// dumpSchemas lists the application schemas in the order a restore needs them.
// The migration connection sets the same three as its search_path, but a dump
// must not depend on session state: every object it writes is schema-qualified.
var dumpSchemas = []string{"public", "internal", "reference"}

// copyStatementPrefix marks the start of a COPY block. The importer finds the
// blocks by looking for this marker at the start of a line, which is how
// backup.go writes them.
const copyStatementPrefix = "COPY "

// copyTerminator ends a COPY block: a backslash and a dot on a line of their
// own, the same marker psql uses.
const copyTerminator = `\.`

// excludedTables lists the tables a data dump never carries.
//
// app_migration is the migrator's own bookkeeping. Its rows are derived from the
// migration files by migrate:up, so carrying them in a dump would either
// duplicate the version table or, worse, claim a schema state the target does
// not have.
var excludedTables = []string{entity.TableAppMigration}

// isExcludedTable reports whether a table is left out of a dump.
func isExcludedTable(table Table) bool {
	return slices.Contains(excludedTables, table.String())
}

// DumpOptions selects what a dump contains. With neither field set the dump
// carries schema and data.
type DumpOptions struct {
	// SchemaOnly writes the DDL and no rows.
	SchemaOnly bool
	// DataOnly writes the rows and no DDL. The target database must already
	// carry the schema, which migrate:up provides.
	DataOnly bool
}

// Table identifies one table in one schema.
type Table struct {
	Schema string
	Name   string
}

// String renders the table for a report.
func (t Table) String() string { return t.Schema + "." + t.Name }

// Qualified renders the table as a quoted SQL identifier.
func (t Table) Qualified() string { return pgx.Identifier{t.Schema, t.Name}.Sanitize() }

// DumpStats summarizes a finished dump or restore.
type DumpStats struct {
	// Statements is the number of DDL statements written.
	Statements int
	// Tables is the number of tables whose data was written or read.
	Tables int
	// Rows is the number of rows written or read across every table.
	Rows int64
}

// TableProgress reports one table as it is dumped or restored, so a caller can
// show which table is in flight.
type TableProgress struct {
	Table Table
	Rows  int64
}

// Connector hands out a pooled connection. datastore.Postgres is the only
// implementation, so a dump never opens a pool of its own.
type Connector interface {
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
}

// querier is the read surface a dump needs, satisfied by both the pool and a
// transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// eachRow runs a query and hands every row to scan. It closes the rows and
// surfaces the iteration error, so a caller never has to.
func eachRow(ctx context.Context, q querier, sql string, args []any, scan func(pgx.Rows) error) error {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		if err := scan(rows); err != nil {
			return fmt.Errorf("database: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("database: %w", err)
	}
	return nil
}

// statementWriter writes DDL sections and counts the statements, so a report
// can say how much was exported without a second pass over the file.
type statementWriter struct {
	w     io.Writer
	count int
}

// write emits one statement or one section heading.
func (s *statementWriter) write(line string) error {
	if _, err := io.WriteString(s.w, line+"\n"); err != nil {
		return err
	}
	s.count++
	return nil
}

// comment emits a section heading. It is not counted as a statement.
func (s *statementWriter) comment(title string) error {
	_, err := fmt.Fprintf(s.w, "\n-- %s\n", title)
	return err
}

// quotedColumns renders each name as a quoted SQL identifier.
func quotedColumns(names []string) []string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = pgx.Identifier{name}.Sanitize()
	}
	return quoted
}
