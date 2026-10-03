package database

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Exporter writes a SQL dump of the application schemas.
type Exporter struct {
	connector Connector
	opts      DumpOptions
}

// NewExporter builds an exporter over an existing pool.
func NewExporter(connector Connector, opts DumpOptions) *Exporter {
	return &Exporter{connector: connector, opts: opts}
}

// Dump writes the dump to w, calling progress after each table. Everything runs
// on one connection inside one transaction, so every COPY reads the same
// snapshot and a concurrent write cannot produce a torn dump.
func (e *Exporter) Dump(ctx context.Context, w io.Writer, progress func(TableProgress)) (DumpStats, error) {
	var stats DumpStats

	conn, err := e.connector.Acquire(ctx)
	if err != nil {
		return stats, err
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return stats, fmt.Errorf("database: begin dump transaction: %w", err)
	}
	// A dump only reads, so an early return needs nothing but a release.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := writeDumpHeader(ctx, w, tx); err != nil {
		return stats, err
	}

	writer := &statementWriter{w: w}
	if !e.opts.DataOnly {
		if err := dumpDDL(ctx, writer, tx); err != nil {
			return stats, err
		}
		stats.Statements = writer.count
	}
	if !e.opts.SchemaOnly {
		rows, tables, err := dumpData(ctx, w, tx, progress)
		if err != nil {
			return stats, err
		}
		stats.Rows, stats.Tables = rows, tables
	}
	return stats, nil
}

// writeDumpHeader names the database, the server, and the moment of the dump.
// Only the version and the database name are recorded: the DSN is never written,
// because a dump is a file that gets copied around.
func writeDumpHeader(ctx context.Context, w io.Writer, tx pgx.Tx) error {
	var server, database string
	err := tx.QueryRow(ctx, "SELECT version(), current_database()").Scan(&server, &database)
	if err != nil {
		return fmt.Errorf("database: read server identity: %w", err)
	}
	// Only the first two words of version() are stable across releases.
	fields := strings.Fields(server)
	if len(fields) > 2 {
		server = strings.Join(fields[:2], " ")
	}

	_, err = fmt.Fprintf(w, "-- saka database dump\n-- server: %s\n-- database: %s\n-- generated: %s\n",
		server, database, time.Now().UTC().Format(time.RFC3339))
	return err
}

// dumpDDL writes the schema. The order is the order a restore needs: schemas
// and extensions first, then the types a column can reference, then tables,
// then everything that attaches to a table.
//
// Objects that belong to an extension are skipped. They arrive with
// CREATE EXTENSION, and writing them again would fail.
func dumpDDL(ctx context.Context, w *statementWriter, tx pgx.Tx) error {
	for _, section := range []func(context.Context, *statementWriter, pgx.Tx) error{
		dumpSchemasSection,
		dumpExtensions,
		dumpEnums,
		dumpSequences,
		dumpTables,
		dumpConstraints,
		dumpIndexes,
		dumpFunctions,
		dumpTriggers,
		dumpComments,
	} {
		if err := section(ctx, w, tx); err != nil {
			return err
		}
	}
	return nil
}

// guardedByTypeCheck wraps a CREATE TYPE in a catalog check, because Postgres
// has no CREATE TYPE IF NOT EXISTS. A type that already exists is left alone.
func guardedByTypeCheck(schema, name, statement string) string {
	return fmt.Sprintf(`DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_type t
    JOIN pg_namespace n ON n.oid = t.typnamespace
    WHERE t.typname = %s AND n.nspname = %s
  ) THEN
    %s
  END IF;
END $$;`, quoteLiteral(name), quoteLiteral(schema), statement)
}

// guardedByConstraintCheck wraps an ALTER TABLE ADD CONSTRAINT in a catalog
// check, because Postgres has no ADD CONSTRAINT IF NOT EXISTS. The check is
// scoped to the table, so a name that is only taken elsewhere does not stop the
// constraint from being created here.
func guardedByConstraintCheck(schema, table, name, statement string) string {
	return fmt.Sprintf(`DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint con
    JOIN pg_class c ON c.oid = con.conrelid
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE con.conname = %s AND c.relname = %s AND n.nspname = %s
  ) THEN
    %s
  END IF;
END $$;`, quoteLiteral(name), quoteLiteral(table), quoteLiteral(schema), statement)
}

// quoteLiteral renders a value as a SQL literal, so a name holding an apostrophe
// cannot break the guard around it.
func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// triggerOrReplace rewrites a CREATE TRIGGER into CREATE OR REPLACE TRIGGER, so
// replaying a dump over an existing schema replaces the trigger instead of
// failing on it. Postgres 14 added the form; pg_get_triggerdef never emits it.
func triggerOrReplace(definition string) string {
	if rest, ok := strings.CutPrefix(definition, "CREATE TRIGGER "); ok {
		return "CREATE OR REPLACE TRIGGER " + rest
	}
	return definition
}

// indexIfNotExists adds IF NOT EXISTS to a CREATE INDEX statement. The
// definition comes from pg_indexes, which never includes the clause, and an
// index that is already there must not fail the restore.
func indexIfNotExists(definition string) string {
	for _, prefix := range []string{"CREATE UNIQUE INDEX ", "CREATE INDEX "} {
		if rest, ok := strings.CutPrefix(definition, prefix); ok {
			return prefix + "IF NOT EXISTS " + rest
		}
	}
	return definition
}

// dumpData writes one COPY block per table. The rows travel through the COPY
// protocol, so the server does the escaping and a value holding a tab, a quote,
// or a newline needs no special handling here.
func dumpData(
	ctx context.Context,
	w io.Writer,
	tx pgx.Tx,
	progress func(TableProgress),
) (int64, int, error) {
	tables, err := listTables(ctx, tx)
	if err != nil {
		return 0, 0, err
	}
	if len(tables) == 0 {
		return 0, 0, nil
	}
	if _, err := io.WriteString(w, "\n-- data\n"); err != nil {
		return 0, 0, err
	}

	var total int64
	written := 0
	for _, table := range tables {
		// Every column name and every ordering key is read before the COPY
		// stream opens. A query cannot run while the connection is streaming
		// rows, and the server rejects it as "conn busy".
		plan, err := planTableCopy(ctx, tx, table)
		if err != nil {
			return total, written, err
		}
		if plan == nil {
			continue
		}

		rows, err := copyTable(ctx, w, tx, table, plan)
		if err != nil {
			return total, written, err
		}
		total += rows
		written++
		if progress != nil {
			progress(TableProgress{Table: table, Rows: rows})
		}
	}
	return total, written, nil
}

// tableCopy is everything a COPY block needs, read before the stream opens.
type tableCopy struct {
	// Columns are the quoted names the block carries.
	Columns []string
	// Select is the statement whose rows the block streams.
	Select string
}

// planTableCopy reads the columns and the ordering key of one table. It returns
// nil when the table has no column to copy, which leaves it out of the dump.
func planTableCopy(ctx context.Context, tx pgx.Tx, table Table) (*tableCopy, error) {
	columns, err := tableColumnNames(ctx, tx, table)
	if err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, nil
	}

	quoted := quotedColumns(columns)
	statement := fmt.Sprintf("SELECT %s FROM %s", strings.Join(quoted, ", "), table.Qualified())

	// A dump is ordered by the primary key when there is one, so two dumps of
	// the same state produce the same file. A table without a primary key has no
	// defined order, which is the best a dump can promise.
	order, err := primaryKeyColumns(ctx, tx, table)
	if err != nil {
		return nil, err
	}
	if len(order) > 0 {
		statement += " ORDER BY " + strings.Join(quotedColumns(order), ", ")
	}
	return &tableCopy{Columns: quoted, Select: statement}, nil
}

// copyTable writes one COPY block and returns the rows it wrote.
func copyTable(ctx context.Context, w io.Writer, tx pgx.Tx, table Table, plan *tableCopy) (int64, error) {
	// The block header names the target for the importer. The data itself comes
	// from COPY ... TO STDOUT, which streams without buffering the table.
	//
	// The format is text, not csv. In text format the server escapes the
	// terminator, so a value that happens to be `\.` cannot be mistaken for the
	// end of the block. CSV leaves it unescaped and would truncate the table.
	header := fmt.Sprintf("\n%s(%s) FROM stdin WITH (FORMAT text);\n",
		copyStatementPrefix+table.Qualified(), strings.Join(plan.Columns, ", "))
	if _, err := io.WriteString(w, header); err != nil {
		return 0, err
	}

	tag, err := tx.Conn().PgConn().CopyTo(ctx, w,
		fmt.Sprintf("COPY (%s) TO STDOUT WITH (FORMAT text)", plan.Select))
	if err != nil {
		return 0, fmt.Errorf("database: copy %s: %w", table, err)
	}
	if _, err := io.WriteString(w, copyTerminator+"\n"); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// listTables returns every table in the dumped schemas.
func listTables(ctx context.Context, tx pgx.Tx) ([]Table, error) {
	var tables []Table
	err := eachRow(ctx, tx, `
		SELECT n.nspname, c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname = ANY($1)
		ORDER BY n.nspname, c.relname`,
		[]any{dumpSchemas}, func(rows pgx.Rows) error {
			var table Table
			if err := rows.Scan(&table.Schema, &table.Name); err != nil {
				return err
			}
			if isExcludedTable(table) {
				return nil
			}
			tables = append(tables, table)
			return nil
		})
	return tables, err
}

// tableColumnNames lists the columns a COPY should carry. A generated column is
// left out: it is computed on the target, and COPY refuses to write it.
func tableColumnNames(ctx context.Context, tx pgx.Tx, table Table) ([]string, error) {
	var columns []string
	err := eachRow(ctx, tx, `
		SELECT attname FROM pg_attribute
		WHERE attrelid = $1::regclass
		  AND attnum > 0
		  AND NOT attisdropped
		  AND attgenerated = ''
		ORDER BY attnum`,
		[]any{table.Qualified()}, func(rows pgx.Rows) error {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			columns = append(columns, name)
			return nil
		})
	return columns, err
}

// primaryKeyColumns returns the primary key columns in key order, or nothing
// when the table has no primary key. The dump orders each table by them, so two
// dumps of the same state produce the same file. A table without a primary key
// has no defined order, which is the best a dump can promise.
func primaryKeyColumns(ctx context.Context, tx pgx.Tx, table Table) ([]string, error) {
	var columns []string
	err := eachRow(ctx, tx, `
		SELECT a.attname
		FROM pg_constraint c
		JOIN unnest(c.conkey) WITH ORDINALITY AS k(attnum, ord) ON true
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum
		WHERE c.conrelid = $1::regclass AND c.contype = 'p'
		ORDER BY k.ord`,
		[]any{table.Qualified()}, func(rows pgx.Rows) error {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			columns = append(columns, name)
			return nil
		})
	return columns, err
}
