package audit

import (
	"database/sql"
	"testing"

	"github.com/riipandi/saka/framework/migration"
)

// openTestMigrators stands the audit set up over the test database.
func openTestMigrators(t *testing.T, db *sql.DB) (*migration.Runner, error) {
	return migration.NewRunner(t.Context(), db, []migration.Set{Schema()}, migration.MigratorOptions{})
}
