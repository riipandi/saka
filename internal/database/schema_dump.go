package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// This file renders the schema half of a dump: one writer per kind of object,
// in the order a restore needs them. Every statement comes from the server's own
// deparse helpers (format_type, pg_get_constraintdef, pg_get_functiondef,
// pg_get_triggerdef, pg_indexes), so a definition is never re-derived here and
// cannot drift from what Postgres stores.
//
// Objects owned by an extension are skipped everywhere. They arrive with
// CREATE EXTENSION, and writing them again would fail.

func dumpSchemasSection(ctx context.Context, w *statementWriter, tx pgx.Tx) error {
	var names []string
	err := eachRow(ctx, tx,
		`SELECT nspname FROM pg_namespace WHERE nspname = ANY($1) ORDER BY nspname`,
		[]any{dumpSchemas}, func(rows pgx.Rows) error {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			names = append(names, name)
			return nil
		})
	if err != nil || len(names) == 0 {
		return err
	}
	if err := w.comment("schemas"); err != nil {
		return err
	}
	for _, name := range names {
		line := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", pgx.Identifier{name}.Sanitize())
		if err := w.write(line); err != nil {
			return err
		}
	}
	return nil
}

func dumpExtensions(ctx context.Context, w *statementWriter, tx pgx.Tx) error {
	var names []string
	// plpgsql is installed by initdb and cannot be dropped, so it is noise.
	err := eachRow(ctx, tx,
		`SELECT extname FROM pg_extension WHERE extname <> 'plpgsql' ORDER BY extname`,
		nil, func(rows pgx.Rows) error {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			names = append(names, name)
			return nil
		})
	if err != nil || len(names) == 0 {
		return err
	}
	if err := w.comment("extensions"); err != nil {
		return err
	}
	for _, name := range names {
		line := fmt.Sprintf("CREATE EXTENSION IF NOT EXISTS %s;", pgx.Identifier{name}.Sanitize())
		if err := w.write(line); err != nil {
			return err
		}
	}
	return nil
}

// dumpEnums writes the enum types before the tables, because a column can
// reference one and Postgres rejects a forward reference.
//
// CREATE TYPE has no IF NOT EXISTS, so each statement is guarded by a DO block
// that asks the catalog first. A dump that is replayed over an existing schema
// would otherwise fail on the first type it meets.
func dumpEnums(ctx context.Context, w *statementWriter, tx pgx.Tx) error {
	written := false
	return eachRow(ctx, tx, `
		SELECT n.nspname, t.typname,
		       string_agg(quote_literal(e.enumlabel), ', ' ORDER BY e.enumsortorder)
		FROM pg_type t
		JOIN pg_enum e ON e.enumtypid = t.oid
		JOIN pg_namespace n ON n.oid = t.typnamespace
		WHERE n.nspname = ANY($1)
		GROUP BY n.nspname, t.typname
		ORDER BY n.nspname, t.typname`,
		[]any{dumpSchemas}, func(rows pgx.Rows) error {
			var schema, name, labels string
			if err := rows.Scan(&schema, &name, &labels); err != nil {
				return err
			}
			if !written {
				if err := w.comment("enum types"); err != nil {
					return err
				}
				written = true
			}
			statement := fmt.Sprintf("CREATE TYPE %s AS ENUM (%s);",
				pgx.Identifier{schema, name}.Sanitize(), labels)
			return w.write(guardedByTypeCheck(schema, name, statement))
		})
}

// dumpSequences writes the sequences the application owns. A sequence created
// by SERIAL or GENERATED belongs to its column and arrives with the table, so
// it is skipped: writing it again would leave two objects claiming one name.
func dumpSequences(ctx context.Context, w *statementWriter, tx pgx.Tx) error {
	written := false
	return eachRow(ctx, tx, `
		SELECT n.nspname, c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'S'
		  AND n.nspname = ANY($1)
		  AND NOT EXISTS (
		      SELECT 1 FROM pg_depend d
		      WHERE d.objid = c.oid
		        AND d.classid = 'pg_class'::regclass
		        AND d.deptype IN ('e', 'a', 'i'))
		ORDER BY n.nspname, c.relname`,
		[]any{dumpSchemas}, func(rows pgx.Rows) error {
			var schema, name string
			if err := rows.Scan(&schema, &name); err != nil {
				return err
			}
			if !written {
				if err := w.comment("sequences"); err != nil {
					return err
				}
				written = true
			}
			line := fmt.Sprintf("CREATE SEQUENCE IF NOT EXISTS %s;", pgx.Identifier{schema, name}.Sanitize())
			return w.write(line)
		})
}

// dumpTables writes one CREATE TABLE per table. Defaults come from the catalog
// rather than being re-derived, so a nextval binding survives the round trip.
func dumpTables(ctx context.Context, w *statementWriter, tx pgx.Tx) error {
	// The table list is read to the end before the columns are read. A query
	// cannot run while another one is still streaming, and the server reports
	// that as "conn busy".
	type tableRef struct {
		oid    uint32
		schema string
		name   string
	}
	var refs []tableRef
	err := eachRow(ctx, tx, `
		SELECT c.oid, n.nspname, c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname = ANY($1)
		ORDER BY n.nspname, c.relname`,
		[]any{dumpSchemas}, func(rows pgx.Rows) error {
			var ref tableRef
			if err := rows.Scan(&ref.oid, &ref.schema, &ref.name); err != nil {
				return err
			}
			if isExcludedTable(Table{Schema: ref.schema, Name: ref.name}) {
				return nil
			}
			refs = append(refs, ref)
			return nil
		})
	if err != nil || len(refs) == 0 {
		return err
	}
	if err := w.comment("tables"); err != nil {
		return err
	}

	for _, ref := range refs {
		columns, err := tableColumns(ctx, tx, ref.oid)
		if err != nil {
			return err
		}
		line := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n  %s\n);",
			pgx.Identifier{ref.schema, ref.name}.Sanitize(), strings.Join(columns, ",\n  "))
		if err := w.write(line); err != nil {
			return err
		}
	}
	return nil
}

// tableColumns renders one column definition per line, using format_type so a
// type keeps its modifier and its schema (character varying(100),
// public.jwt_algorithm, text[]).
func tableColumns(ctx context.Context, tx pgx.Tx, oid uint32) ([]string, error) {
	var columns []string
	err := eachRow(ctx, tx, `
		SELECT a.attname,
		       format_type(a.atttypid, a.atttypmod),
		       a.attnotnull,
		       pg_get_expr(d.adbin, d.adrelid)
		FROM pg_attribute a
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`,
		[]any{oid}, func(rows pgx.Rows) error {
			var name, dataType string
			var notNull bool
			var defaultExpr *string
			if err := rows.Scan(&name, &dataType, &notNull, &defaultExpr); err != nil {
				return err
			}
			column := fmt.Sprintf("%s %s", pgx.Identifier{name}.Sanitize(), dataType)
			if defaultExpr != nil {
				column += " DEFAULT " + *defaultExpr
			}
			if notNull {
				column += " NOT NULL"
			}
			columns = append(columns, column)
			return nil
		})
	return columns, err
}

// dumpConstraints writes primary, unique, check, and foreign keys as ALTER
// TABLE. They run after every table exists, so a foreign key never points at a
// table that is still missing.
//
// The order inside the section matters. A foreign key can only reference a
// column that is already unique, so the primary and unique keys are added first
// and the foreign keys last. Sorting by contype alone would put them
// alphabetically (c, f, p, u) and Postgres would reject the foreign keys.
//
// ALTER TABLE ADD CONSTRAINT has no IF NOT EXISTS, so each statement is guarded
// by a DO block that asks the catalog first. A dump replayed over an existing
// schema would otherwise fail on the first constraint it meets.
func dumpConstraints(ctx context.Context, w *statementWriter, tx pgx.Tx) error {
	written := false
	return eachRow(ctx, tx, `
		SELECT n.nspname, c.relname, con.conname, pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE con.contype IN ('p', 'u', 'c', 'f') AND n.nspname = ANY($1)
		ORDER BY CASE con.contype
		             WHEN 'p' THEN 1
		             WHEN 'u' THEN 2
		             WHEN 'c' THEN 3
		             ELSE 4
		         END,
		         n.nspname, c.relname, con.conname`,
		[]any{dumpSchemas}, func(rows pgx.Rows) error {
			var schema, table, name, definition string
			if err := rows.Scan(&schema, &table, &name, &definition); err != nil {
				return err
			}
			if isExcludedTable(Table{Schema: schema, Name: table}) {
				return nil
			}
			if !written {
				if err := w.comment("constraints"); err != nil {
					return err
				}
				written = true
			}
			statement := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s;",
				pgx.Identifier{schema, table}.Sanitize(),
				pgx.Identifier{name}.Sanitize(), definition)
			return w.write(guardedByConstraintCheck(schema, table, name, statement))
		})
}

// dumpIndexes writes the indexes no constraint already created. A primary key or
// unique constraint has its own index, and writing that index again would fail
// on a duplicate name.
func dumpIndexes(ctx context.Context, w *statementWriter, tx pgx.Tx) error {
	written := false
	return eachRow(ctx, tx, `
		SELECT schemaname, tablename, indexdef FROM pg_indexes
		WHERE schemaname = ANY($1)
		  AND indexname NOT IN (
		      SELECT conname FROM pg_constraint WHERE contype IN ('p', 'u'))
		ORDER BY schemaname, tablename, indexname`,
		[]any{dumpSchemas}, func(rows pgx.Rows) error {
			var schema, table, definition string
			if err := rows.Scan(&schema, &table, &definition); err != nil {
				return err
			}
			if isExcludedTable(Table{Schema: schema, Name: table}) {
				return nil
			}
			if !written {
				if err := w.comment("indexes"); err != nil {
					return err
				}
				written = true
			}
			return w.write(indexIfNotExists(definition) + ";")
		})
}

// dumpFunctions writes the functions the application owns. pg_get_functiondef
// renders the whole body, including the language and the volatility, so nothing
// about a function is re-derived here. Extension functions are skipped: they
// arrive with CREATE EXTENSION.
func dumpFunctions(ctx context.Context, w *statementWriter, tx pgx.Tx) error {
	written := false
	return eachRow(ctx, tx, `
		SELECT pg_get_functiondef(p.oid)
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = ANY($1)
		  AND NOT EXISTS (
		      SELECT 1 FROM pg_depend d
		      WHERE d.objid = p.oid
		        AND d.classid = 'pg_proc'::regclass
		        AND d.deptype = 'e')
		ORDER BY n.nspname, p.proname`,
		[]any{dumpSchemas}, func(rows pgx.Rows) error {
			var definition string
			if err := rows.Scan(&definition); err != nil {
				return err
			}
			if !written {
				if err := w.comment("functions"); err != nil {
					return err
				}
				written = true
			}
			return w.write(strings.TrimRight(definition, "\n") + ";")
		})
}

// dumpTriggers writes the user triggers. A trigger Postgres created to enforce a
// constraint is internal and is already covered by that constraint.
//
// CREATE OR REPLACE TRIGGER (Postgres 14 and later) makes a replay idempotent,
// which is why the definition from pg_get_triggerdef is rewritten rather than
// used as it comes.
func dumpTriggers(ctx context.Context, w *statementWriter, tx pgx.Tx) error {
	written := false
	return eachRow(ctx, tx, `
		SELECT n.nspname, c.relname, pg_get_triggerdef(t.oid)
		FROM pg_trigger t
		JOIN pg_class c ON c.oid = t.tgrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE NOT t.tgisinternal AND n.nspname = ANY($1)
		ORDER BY n.nspname, c.relname, t.tgname`,
		[]any{dumpSchemas}, func(rows pgx.Rows) error {
			var schema, table, definition string
			if err := rows.Scan(&schema, &table, &definition); err != nil {
				return err
			}
			if isExcludedTable(Table{Schema: schema, Name: table}) {
				return nil
			}
			if !written {
				if err := w.comment("triggers"); err != nil {
					return err
				}
				written = true
			}
			return w.write(triggerOrReplace(definition) + ";")
		})
}

// dumpComments writes table and column comments. The description is quoted by
// the server, so a comment holding an apostrophe cannot break the statement.
func dumpComments(ctx context.Context, w *statementWriter, tx pgx.Tx) error {
	written := false
	return eachRow(ctx, tx, `
		SELECT n.nspname, c.relname, a.attname, quote_literal(d.description)
		FROM pg_description d
		JOIN pg_class c ON c.oid = d.objoid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = d.objsubid
		WHERE n.nspname = ANY($1)
		  AND c.relkind = 'r'
		  AND (d.objsubid = 0 OR (a.attnum IS NOT NULL AND a.attnum > 0))
		ORDER BY n.nspname, c.relname, d.objsubid`,
		[]any{dumpSchemas}, func(rows pgx.Rows) error {
			var schema, table, literal string
			var column *string
			if err := rows.Scan(&schema, &table, &column, &literal); err != nil {
				return err
			}
			if isExcludedTable(Table{Schema: schema, Name: table}) {
				return nil
			}
			if !written {
				if err := w.comment("comments"); err != nil {
					return err
				}
				written = true
			}
			if column == nil {
				line := fmt.Sprintf("COMMENT ON TABLE %s IS %s;",
					pgx.Identifier{schema, table}.Sanitize(), literal)
				return w.write(line)
			}
			line := fmt.Sprintf("COMMENT ON COLUMN %s.%s IS %s;",
				pgx.Identifier{schema, table}.Sanitize(),
				pgx.Identifier{*column}.Sanitize(), literal)
			return w.write(line)
		})
}
