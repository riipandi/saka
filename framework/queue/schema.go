package queue

import (
	"embed"
	"io/fs"

	"github.com/riipandi/saka/framework/migration"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Schema is the set this package's tables travel in: the pending table, the
// archive, and their indexes. A binary composes it with the other sets it
// runs.
func Schema() migration.Set {
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic("queue: open embedded migrations: " + err.Error())
	}
	return migration.Set{
		Name:         "queue",
		FS:           files,
		VersionTable: "queue_migrations",
		LockKey:      7211043277442,
	}
}
