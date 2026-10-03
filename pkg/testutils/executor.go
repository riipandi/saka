package testutils

import (
	"context"
	"fmt"
	"testing"

	tc "github.com/testcontainers/testcontainers-go"
)

// Paths used inside the container for dump/restore/import file exchange.
const (
	containerDumpOutPath   = "/tmp/saka-dump.out"
	containerRestoreInPath = "/tmp/saka-restore.dump"
	containerSQLInPath     = "/tmp/saka-import.sql"
)

// ContainerExecutor runs the PostgreSQL client tools inside the test
// Postgres container. It structurally satisfies database.Executor, so
// tests can override database.DefaultExecutor without an import cycle.
//
// Host/port flags are stripped so the tools connect over the
// container-local Unix socket (trusted auth); env is ignored for the
// same reason. Files cross the boundary via container copy.
type ContainerExecutor struct {
	PG *Postgres
}

// stripHostPort removes -h/--host and -p/--port (with their values) so
// the client tools use the Unix socket inside the container.
func stripHostPort(args []string) []string {
	result := make([]string, 0, len(args))
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		switch arg {
		case "-h", "--host", "-p", "--port":
			skipNext = true
			continue
		}
		if len(arg) > 3 && (arg[:3] == "-h=" || arg[:6] == "--host") {
			continue
		}
		if len(arg) > 3 && (arg[:3] == "-p=" || arg[:6] == "--port") {
			continue
		}
		result = append(result, arg)
	}
	return result
}

// PGDump dumps to a path inside the container, then copies the file to
// the host output path.
func (e *ContainerExecutor) PGDump(ctx context.Context, _ []string, args []string, outputPath string) error {
	args = stripHostPort(args)
	return e.PG.execPGDump(ctx, args, outputPath)
}

// PGRestore copies the dump file (the last argument) into the
// container, then restores from it.
func (e *ContainerExecutor) PGRestore(ctx context.Context, _ []string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("pg_restore: no dump file argument")
	}
	dumpFile := args[len(args)-1]
	if err := e.PG.CopyFileToContainer(ctx, dumpFile, containerRestoreInPath); err != nil {
		return fmt.Errorf("copy dump to container: %w", err)
	}
	args = stripHostPort(append(args[:len(args)-1:len(args)-1], containerRestoreInPath))
	return e.PG.execPGRestore(ctx, args)
}

// PSQL copies the SQL file (the value of -f) into the container, then
// runs psql against it.
func (e *ContainerExecutor) PSQL(ctx context.Context, _ []string, args []string) error {
	for i, arg := range args {
		if arg == "-f" && i+1 < len(args) {
			if err := e.PG.CopyFileToContainer(ctx, args[i+1], containerSQLInPath); err != nil {
				return fmt.Errorf("copy sql file to container: %w", err)
			}
			args = append(append(args[:i:i+1], "-f", containerSQLInPath), args[i+2:]...)
			break
		}
	}
	args = stripHostPort(args)
	return e.PG.execPSQL(ctx, args)
}

// SkipWithoutDocker skips the test when no healthy Docker provider is
// available. This lets container-based integration tests skip on hosts
// without a daemon — e.g. GitHub-hosted macOS runners, which lack
// nested virtualization and cannot run a Linux VM for Docker. It
// mirrors testcontainers.SkipIfProviderIsNotHealthy but accepts
// testing.TB.
func SkipWithoutDocker(t testing.TB) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Skipf("docker provider unavailable: %v", r)
		}
	}()

	provider, err := tc.ProviderDocker.GetProvider()
	if err != nil {
		t.Skipf("docker is not running, skipping container test: %s", err)
	}
	err = provider.Health(context.Background())
	if err != nil {
		t.Skipf("docker is not running, skipping container test: %s", err)
	}
}
