package testutils

import (
	"context"
	"strings"
	"testing"
)

// TestTheTestContainerTracksItsStatements proves the dev and test posture's
// pg_stat_statements capability (plan-20261014_0604/D7): the container boots
// preloaded, the extension exists in the seed database, and the stats view
// answers a query.
func TestTheTestContainerTracksItsStatements(t *testing.T) {
	ctx := context.Background()
	pg := StartPostgres(ctx, t)

	exitCode, output, err := pg.Exec(ctx, []string{
		"psql", "-U", "postgres", "-d", "saka_test", "-t", "-A", "-c",
		`SELECT count(*) FROM pg_stat_statements WHERE dbid = (SELECT oid FROM pg_database WHERE datname = 'saka_test')`,
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("the statements view does not answer (exit %d): %s", exitCode, strings.TrimSpace(output))
	}
	if strings.TrimSpace(output) == "" || strings.TrimSpace(output) == "0" {
		t.Fatalf("the statements view answers but tracks nothing: %q", strings.TrimSpace(output))
	}
}
