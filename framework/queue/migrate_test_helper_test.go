package queue

import (
	"database/sql"
	"testing"

	"github.com/riipandi/saka/framework/migration"
)

// openTestMigrators stands the queue's own set up over the test database.
// The fixture agreement with the real DDL is the migration file itself.
func openTestMigrators(t *testing.T, db *sql.DB) (*migration.Runner, error) {
	return migration.NewRunner(t.Context(), db, []migration.Set{Schema()}, migration.MigratorOptions{})
}
