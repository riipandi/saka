package scheduler

import (
	"embed"
	"io/fs"

	"github.com/riipandi/saka/framework/migration"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Schema is the set this package's state table travels in. A binary composes
// it with the other sets it runs.
func Schema() migration.Set {
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic("scheduler: open embedded migrations: " + err.Error())
	}
	return migration.Set{
		Name:         "scheduler",
		FS:           files,
		VersionTable: "scheduler_migrations",
		LockKey:      7211043277443,
	}
}
