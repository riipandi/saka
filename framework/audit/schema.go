package audit

import (
	"embed"
	"io/fs"

	"github.com/riipandi/saka/framework/migration"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Schema is the set this package's table travels in: the audit log's enums,
// its table, and its indexes. A binary composes it with the other sets it
// runs.
func Schema() migration.Set {
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic("audit: open embedded migrations: " + err.Error())
	}
	return migration.Set{
		Name:         "audit",
		FS:           files,
		VersionTable: "audit_migrations",
		LockKey:      7211043277441,
	}
}
