package database

import (
	"embed"
	"io/fs"

	"github.com/riipandi/saka/framework/migration"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsDir is the directory inside the embedded filesystem.
const migrationsDir = "migrations"

// MigrationsPath is where the application's migration files live on disk,
// relative to the module root. The embedded copy that ships in a binary is
// built from this directory, so a new file only reaches the migrator after
// a rebuild.
const MigrationsPath = "internal/database/" + migrationsDir

// VersionTable records the applied migrations of the app set. goose defaults
// to goose_db_version; the project owns the name so the schema reads as ours.
const VersionTable = migration.AppVersionTable

// Schema is the application's migration set — the tables every feature's
// feature owns and the seeders' shape. A binary composes it with the sets
// the framework packages carry.
func Schema() migration.Set {
	files, err := fs.Sub(migrationFiles, migrationsDir)
	if err != nil {
		// The embed pattern either matches or the build fails, so this is
		// not a runtime condition.
		panic("database: open embedded migrations: " + err.Error())
	}
	return migration.Set{
		Name:         "app",
		FS:           files,
		VersionTable: migration.AppVersionTable,
	}
}

// Validate checks the embedded migrations for the structural mistakes goose
// rejects while applying. It reads nothing outside the binary and needs no
// database, so it runs in CI before a connection exists.
func Validate() migration.ValidationReport {
	return migration.ValidateFS(Schema().FS)
}

// EmbeddedMigrations returns the migrations compiled into this binary, in
// version order. Tests and tooling derive their expectations from this list
// instead of hardcoding counts that go stale with every new file.
func EmbeddedMigrations() ([]migration.EmbeddedMigration, error) {
	return migration.EmbeddedMigrations(Schema().FS)
}
