package scheduler

import (
	"database/sql"
	"testing"

	"github.com/riipandi/saka/framework/migration"
	fwqueue "github.com/riipandi/saka/framework/queue"
)

// openTestMigrators stands the sets the scheduler touches up over the test
// database: its own state table and the queue tables a claimed tick fills.
func openTestMigrators(t *testing.T, db *sql.DB) (*migration.Runner, error) {
	return migration.NewRunner(t.Context(), db, []migration.Set{fwqueue.Schema(), Schema()}, migration.MigratorOptions{})
}
