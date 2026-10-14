package testutils

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/exec"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Postgres is a running Postgres container for integration tests.
type Postgres struct {
	// DSN is the connection string of the shared instance.
	DSN       string
	container *tcpg.PostgresContainer
}

// postgresImage is the Postgres image used by integration tests.
const postgresImage = "docker.io/postgres:18-alpine"

// postgresConf and postgresInit are the container boot files for the dev and
// test posture (plan-20261014_0604/D7): the statements module is preloaded so
// every query the container runs is tracked, and the extension exists in the
// seed database the shared DSN names. Deployment images do not carry the
// preload, so this is a test-only capability — never a migration.
const postgresConf = `
listen_addresses = '*'
shared_preload_libraries = 'pg_stat_statements'
pg_stat_statements.track = all
`

const postgresInit = `CREATE EXTENSION IF NOT EXISTS pg_stat_statements;`

var (
	postgresOnce   sync.Once
	sharedPostgres *Postgres
	sharedPGErr    error
)

// StartPostgres returns the shared Postgres container for the test binary.
func StartPostgres(ctx context.Context, t testing.TB) *Postgres {
	t.Helper()

	SkipWithoutDocker(t)

	postgresOnce.Do(func() {
		sharedPostgres, sharedPGErr = startPostgres(ctx)
	})

	require.NoError(t, sharedPGErr, "start postgres container (docker daemon required)")
	return sharedPostgres
}

// startPostgres starts the container. BasicWaitStrategies waits for the
// "ready to accept connections" log twice (the image restarts once during
// init) and then for the port to be served — required for reliable startup
// on Docker Desktop proxies (macOS/Windows).
func startPostgres(ctx context.Context) (*Postgres, error) {
	boot, err := writePostgresBootFiles()
	if err != nil {
		return nil, fmt.Errorf("write postgres boot files: %w", err)
	}

	container, err := tcpg.Run(ctx, postgresImage,
		tcpg.WithDatabase("saka_test"),
		tcpg.WithUsername("postgres"),
		tcpg.WithPassword("postgres"),
		tcpg.WithConfigFile(boot.conf),
		tcpg.WithInitScripts(boot.init),
		tcpg.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("run postgres container: %w", err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("resolve postgres endpoint: %w", err)
	}
	if err := pingPostgres(ctx, dsn); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Postgres{DSN: dsn, container: container}, nil
}

// pingPostgres connects until the server answers or the budget runs out. The
// wait strategies watch the image's own logs, but the init scripts extend the
// first boot: a caller that connects the moment the port listens can race the
// restart the image does between the init phase and the real server, and the
// connection dies with a reset. The ping is the readiness that matters.
func pingPostgres(ctx context.Context, dsn string) error {
	const attempts = 60
	var lastErr error
	for i := 0; i < attempts; i++ {
		conn, err := pgx.Connect(ctx, dsn)
		if err == nil {
			_ = conn.Close(ctx)
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("the server never answered a connection after %d attempts: %w", attempts, lastErr)
}

// postgresBootFiles names the conf and the init script the container mounts.
type postgresBootFiles struct {
	conf string
	init string
}

// writePostgresBootFiles stages the statement-tracking conf and the extension
// init script in a temp directory the container mounts at boot. The files
// outlive the tests that run them; a stale directory is harmless — each
// container boot mounts its own.
func writePostgresBootFiles() (postgresBootFiles, error) {
	dir, err := os.MkdirTemp("", "saka-pg-boot-")
	if err != nil {
		return postgresBootFiles{}, fmt.Errorf("create boot dir: %w", err)
	}
	confPath := filepath.Join(dir, "postgres.conf")
	if err := os.WriteFile(confPath, []byte(postgresConf), 0o644); err != nil {
		return postgresBootFiles{}, fmt.Errorf("write postgres.conf: %w", err)
	}
	initPath := filepath.Join(dir, "001-pg-stat-statements.sql")
	if err := os.WriteFile(initPath, []byte(postgresInit), 0o644); err != nil {
		return postgresBootFiles{}, fmt.Errorf("write the init script: %w", err)
	}
	return postgresBootFiles{conf: confPath, init: initPath}, nil
}

// Exec runs a command inside the Postgres container and returns the exit code
// and combined output. The command runs as the postgres user.
func (p *Postgres) Exec(ctx context.Context, cmd []string) (int, string, error) {
	if p.container == nil {
		return -1, "", fmt.Errorf("container not available")
	}
	exitCode, outputReader, err := p.container.Exec(ctx, cmd, exec.WithUser("postgres"), exec.Multiplexed())
	if err != nil {
		return exitCode, "", err
	}
	output, err := io.ReadAll(outputReader)
	return exitCode, string(output), err
}

// CopyFileFromContainer copies a file from the container to the host.
func (p *Postgres) CopyFileFromContainer(ctx context.Context, containerPath, hostPath string) error {
	if p.container == nil {
		return fmt.Errorf("container not available")
	}
	reader, err := p.container.CopyFileFromContainer(ctx, containerPath)
	if err != nil {
		return fmt.Errorf("copy file from container: %w", err)
	}
	defer func() { _ = reader.Close() }()

	err = os.MkdirAll(filepath.Dir(hostPath), 0o755)
	if err != nil {
		return fmt.Errorf("create host dir: %w", err)
	}

	f, err := os.Create(hostPath)
	if err != nil {
		return fmt.Errorf("create host file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, reader); err != nil {
		return fmt.Errorf("write host file: %w", err)
	}
	return nil
}

// execPGDump runs pg_dump inside the container, writing to a container
// path, then copies the result to the host outputPath.
func (p *Postgres) execPGDump(ctx context.Context, args []string, outputPath string) error {
	cmd := append([]string{"pg_dump"}, args...)
	cmd = append(cmd, "-f", containerDumpOutPath)
	if err := p.runPGTool(ctx, "pg_dump", cmd); err != nil {
		return err
	}
	return p.CopyFileFromContainer(ctx, containerDumpOutPath, outputPath)
}

// execPGRestore runs pg_restore inside the container. The dump file
// must already be copied into the container.
func (p *Postgres) execPGRestore(ctx context.Context, args []string) error {
	return p.runPGTool(ctx, "pg_restore", append([]string{"pg_restore"}, args...))
}

// execPSQL runs psql inside the container. The SQL file must already be
// copied into the container.
func (p *Postgres) execPSQL(ctx context.Context, args []string) error {
	return p.runPGTool(ctx, "psql", append([]string{"psql"}, args...))
}

// runPGTool executes a client binary inside the container and fails on
// a non-zero exit code.
func (p *Postgres) runPGTool(ctx context.Context, name string, cmd []string) error {
	exitCode, output, err := p.Exec(ctx, cmd)
	if err != nil {
		return fmt.Errorf("%s exec error: %w", name, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("%s failed (exit %d): %s", name, exitCode, output)
	}
	return nil
}

// CopyFileToContainer copies a file from host to container.
func (p *Postgres) CopyFileToContainer(ctx context.Context, hostPath, containerPath string) error {
	if p.container == nil {
		return fmt.Errorf("container not available")
	}
	return p.container.CopyFileToContainer(ctx, hostPath, containerPath, 0o644)
}

// ContainerID returns the container ID for debugging.
func (p *Postgres) ContainerID() string {
	if p.container == nil {
		return ""
	}
	return p.container.GetContainerID()
}
