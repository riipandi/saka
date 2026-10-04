package database

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ErrNoCopyBlocks is returned when a dump carries no COPY block, which means it
// has nothing to restore.
var ErrNoCopyBlocks = errors.New("database: no COPY block found in the dump")

// copyHeaderPattern matches the COPY header backup.go writes:
//
//	COPY "public"."users" ("id", "email") FROM stdin WITH (FORMAT text);
//
// The header is the contract between export and import. It names the target and
// its columns, so a restore does not have to guess either.
var copyHeaderPattern = regexp.MustCompile(
	`^COPY\s+("[^"]+"(?:\."[^"]+")?)\s*\(([^)]*)\)\s+FROM\s+stdin`)

// Restorer loads a SQL dump written by Exporter.
type Restorer struct {
	connector Connector
	truncate  bool
}

// NewRestorer builds a restorer over an existing pool. With truncate set, every
// table in the dump is emptied before its rows are loaded.
func NewRestorer(connector Connector, truncate bool) *Restorer {
	return &Restorer{connector: connector, truncate: truncate}
}

// Restore reads r and loads every COPY block it finds. DDL in the file is
// ignored: the target database gets its schema from migrate:up, so replaying the
// DDL too would be a second, competing source of truth.
//
// The whole load runs in one transaction on one connection, so a failure half
// way leaves the target untouched.
func (r *Restorer) Restore(
	ctx context.Context,
	rd io.Reader,
	progress func(TableProgress),
) (DumpStats, error) {
	var stats DumpStats

	conn, err := r.connector.Acquire(ctx)
	if err != nil {
		return stats, err
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return stats, fmt.Errorf("database: begin restore transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	stats, err = r.load(ctx, tx, rd, progress)
	if err != nil {
		return stats, err
	}
	if err := tx.Commit(ctx); err != nil {
		return stats, fmt.Errorf("database: commit restore: %w", err)
	}
	return stats, nil
}

// pendingBlock is one COPY block held in memory until the load can run it.
type pendingBlock struct {
	table   Table
	columns []string
	data    []byte
}

// load walks the dump and copies every block into its table.
//
// The blocks are read before any of them is loaded, and that is deliberate. A
// foreign key is checked per row as it arrives, and a dump is written in
// alphabetical order, so a table can appear before the table it references.
// Reading first allows the load order to be computed from the actual foreign
// keys. The cost is that the data of a dump is held in memory, which is the
// right trade for a developer tool that restores development and staging
// databases.
func (r *Restorer) load(
	ctx context.Context,
	tx pgx.Tx,
	rd io.Reader,
	progress func(TableProgress),
) (DumpStats, error) {
	var stats DumpStats

	blocks, err := readBlocks(rd)
	if err != nil {
		return stats, err
	}
	if len(blocks) == 0 {
		return stats, ErrNoCopyBlocks
	}

	// Every table is emptied before any row is loaded. A TRUNCATE of a table
	// another one references cascades, so emptying one after another had been
	// loaded could delete the rows that were just written.
	if r.truncate {
		for _, block := range blocks {
			if truncateErr := truncateTable(ctx, tx, block.table); truncateErr != nil {
				return stats, truncateErr
			}
		}
	}

	ordered, err := orderByForeignKeys(ctx, tx, blocks)
	if err != nil {
		return stats, err
	}

	for _, block := range ordered {
		rows, err := copyBlock(ctx, tx, block)
		if err != nil {
			return stats, err
		}
		stats.Rows += rows
		stats.Tables++
		if progress != nil {
			progress(TableProgress{Table: block.table, Rows: rows})
		}
	}
	return stats, nil
}

// readBlocks parses the whole dump into memory, in the order it appears.
func readBlocks(rd io.Reader) ([]pendingBlock, error) {
	var blocks []pendingBlock
	reader := bufio.NewReaderSize(rd, 64*1024)

	for {
		header, err := nextCopyHeader(reader)
		if errors.Is(err, io.EOF) {
			return blocks, nil
		}
		if err != nil {
			return nil, err
		}

		table, columns, err := parseCopyHeader(header)
		if err != nil {
			return nil, err
		}

		data, err := readBlockData(reader)
		if err != nil {
			return nil, fmt.Errorf("database: read dump for %s: %w", table, err)
		}
		blocks = append(blocks, pendingBlock{table: table, columns: columns, data: data})
	}
}

// nextCopyHeader advances to the next COPY header and returns it. Anything else
// in the file, including DDL, is skipped.
func nextCopyHeader(reader *bufio.Reader) (string, error) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trimmed, copyStatementPrefix) {
			return trimmed, nil
		}
		if err != nil {
			return "", err
		}
	}
}

// readBlockData collects the rows of one block up to the terminator line. The
// terminator itself is consumed and left out, so the reader is positioned on the
// statement after the block.
func readBlockData(reader *bufio.Reader) ([]byte, error) {
	var data bytes.Buffer
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && len(line) == 0 {
			// The file ended before the terminator did. Accepting that would
			// import a partial table without a word.
			if errors.Is(err, io.EOF) {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if isTerminator(line) {
			return data.Bytes(), nil
		}
		data.Write(line)
		if err != nil {
			return nil, err
		}
	}
}

// parseCopyHeader splits a header into its target and its columns.
func parseCopyHeader(header string) (Table, []string, error) {
	match := copyHeaderPattern.FindStringSubmatch(header)
	if match == nil {
		return Table{}, nil, fmt.Errorf("database: unrecognized COPY header: %s", header)
	}

	schema, name, err := splitQualified(match[1])
	if err != nil {
		return Table{}, nil, err
	}

	rawColumns := strings.Split(match[2], ",")
	columns := make([]string, 0, len(rawColumns))
	for _, raw := range rawColumns {
		column, err := unquoteIdentifier(strings.TrimSpace(raw))
		if err != nil {
			return Table{}, nil, err
		}
		columns = append(columns, column)
	}
	if len(columns) == 0 {
		return Table{}, nil, fmt.Errorf("database: COPY header names no column: %s", header)
	}
	return Table{Schema: schema, Name: name}, columns, nil
}

// splitQualified splits a quoted qualified name into its schema and its name.
// pgx.Identifier.Sanitize joins the parts with a dot, so the dot is the
// separator and each part keeps its own quotes.
func splitQualified(qualified string) (string, string, error) {
	parts := strings.SplitN(qualified, ".", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("database: expected a schema-qualified name: %s", qualified)
	}
	schema, err := unquoteIdentifier(parts[0])
	if err != nil {
		return "", "", err
	}
	name, err := unquoteIdentifier(parts[1])
	if err != nil {
		return "", "", err
	}
	return schema, name, nil
}

// unquoteIdentifier removes the surrounding double quotes and undoes the
// doubling that pgx.Identifier.Sanitize applies to an embedded quote.
func unquoteIdentifier(quoted string) (string, error) {
	if len(quoted) < 2 || !strings.HasPrefix(quoted, `"`) || !strings.HasSuffix(quoted, `"`) {
		return "", fmt.Errorf("database: expected a quoted identifier: %s", quoted)
	}
	inner := quoted[1 : len(quoted)-1]
	return strings.ReplaceAll(inner, `""`, `"`), nil
}

// orderByForeignKeys sorts the blocks so a referenced table is loaded before the
// table that references it. Blocks whose relative order the foreign keys do not
// constrain keep the order they had in the file.
func orderByForeignKeys(ctx context.Context, tx pgx.Tx, blocks []pendingBlock) ([]pendingBlock, error) {
	parents, err := foreignKeyParents(ctx, tx)
	if err != nil {
		return nil, err
	}

	// Keep only the dependencies that both ends of the edge are loading, so an
	// edge to a table outside the dump cannot stall the order.
	loaded := make(map[string]bool, len(blocks))
	for _, block := range blocks {
		loaded[block.table.String()] = true
	}

	waiting := make(map[string]map[string]bool, len(blocks))
	for _, block := range blocks {
		dependencies := map[string]bool{}
		for _, parent := range parents[block.table.String()] {
			if loaded[parent] {
				dependencies[parent] = true
			}
		}
		waiting[block.table.String()] = dependencies
	}

	ordered := make([]pendingBlock, 0, len(blocks))
	done := make(map[string]bool, len(blocks))

	// Repeatedly emit the earliest block whose parents are all loaded. A pass
	// that emits nothing means the remaining blocks form a cycle, which no order
	// can satisfy: the earliest one is emitted so the load makes progress and
	// the database reports the real problem.
	for len(ordered) < len(blocks) {
		emitted := false
		for _, block := range blocks {
			key := block.table.String()
			if done[key] || len(waiting[key]) > 0 {
				continue
			}
			ordered = append(ordered, block)
			done[key] = true
			emitted = true

			for other := range waiting {
				delete(waiting[other], key)
			}
		}
		if emitted {
			continue
		}

		for _, block := range blocks {
			key := block.table.String()
			if done[key] {
				continue
			}
			ordered = append(ordered, block)
			done[key] = true
			for other := range waiting {
				delete(waiting[other], key)
			}
			break
		}
	}
	return ordered, nil
}

// foreignKeyParents maps each table to the tables it references, by qualified
// name. A self-reference is left out: a table cannot be loaded after itself.
func foreignKeyParents(ctx context.Context, tx pgx.Tx) (map[string][]string, error) {
	parents := make(map[string][]string)
	err := eachRow(ctx, tx, `
		SELECT nc.nspname || '.' || c.relname, np.nspname || '.' || p.relname
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_class p ON p.oid = con.confrelid
		JOIN pg_namespace nc ON nc.oid = c.relnamespace
		JOIN pg_namespace np ON np.oid = p.relnamespace
		WHERE con.contype = 'f'
		  AND nc.nspname = ANY($1)
		  AND np.nspname = ANY($1)
		  AND c.oid <> p.oid
		ORDER BY nc.nspname, c.relname, p.relname`,
		[]any{dumpSchemas}, func(rows pgx.Rows) error {
			var child, parent string
			if err := rows.Scan(&child, &parent); err != nil {
				return err
			}
			parents[child] = append(parents[child], parent)
			return nil
		})
	return parents, err
}

// truncateTable empties a table before its rows are loaded, so a restore into a
// populated database replaces the data instead of colliding with it. TRUNCATE
// on a table another table references cascades, which is the only way Postgres
// accepts it.
func truncateTable(ctx context.Context, tx pgx.Tx, table Table) error {
	_, err := tx.Exec(ctx, "TRUNCATE TABLE "+table.Qualified()+" CASCADE")
	if err != nil {
		return fmt.Errorf("database: truncate %s: %w", table, err)
	}
	return nil
}

// copyBlock streams one block into its table.
func copyBlock(ctx context.Context, tx pgx.Tx, block pendingBlock) (int64, error) {
	quoted := quotedColumns(block.columns)
	statement := fmt.Sprintf("%s%s(%s) FROM stdin",
		copyStatementPrefix, block.table.Qualified(), strings.Join(quoted, ", "))

	tag, err := tx.Conn().PgConn().CopyFrom(ctx, bytes.NewReader(block.data), statement)
	if err != nil {
		return 0, fmt.Errorf("database: copy into %s: %w", block.table, err)
	}
	return tag.RowsAffected(), nil
}

// isTerminator reports whether a line is the end-of-data marker.
func isTerminator(line []byte) bool {
	return string(bytes.TrimRight(line, "\r\n")) == copyTerminator
}
