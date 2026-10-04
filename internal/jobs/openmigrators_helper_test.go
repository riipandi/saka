package jobs

import (
	"database/sql"
	"testing"

	"github.com/riipandi/saka/framework/migration"
	"github.com/riipandi/saka/internal/testutils"
)

// openTestMigrators migrates the composed sets over the test database handle.
func openTestMigrators(t *testing.T, db *sql.DB) (*migration.Runner, error) {
	return testutils.TestMigrator(t, db), nil
}
