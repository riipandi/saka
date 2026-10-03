package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/huandu/go-sqlbuilder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/authz"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/pkg/crypto"
	"github.com/riipandi/saka/pkg/testutils"
)

// runInitializeCmd runs one command against the test root, so the flags and
// the writer behave the way the real binary's do.
func runInitializeCmd(t *testing.T, cmd *cli.Command, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	root := testRoot(&out, "", cmd)
	err := root.Run(context.Background(), append([]string{"saka", cmd.Name}, args...))
	return out.String(), err
}

// initializedDatabase migrates a throwaway database and answers the pieces
// the two deployment commands read through.
type initializedDatabase struct {
	dsn     string
	envFile string
}

func freshMigratedDatabase(t *testing.T) initializedDatabase {
	t.Helper()

	container := testutils.StartPostgres(t.Context(), t)
	dsn := container.NewDatabase(t)
	envFile := writeEnvFile(t, dsn)
	_, err := runMigrateUpCmd(t, "", "--env-file="+envFile, "--force")
	require.NoError(t, err)
	// After the migration run: it writes its own database-only config file,
	// and initialize reads the deployment one — both secrets plus the DSN.
	configForDeployment(t)
	return initializedDatabase{dsn: dsn, envFile: envFile}
}

func TestInitializePreparesAFreshDatabase(t *testing.T) {
	db := freshMigratedDatabase(t)

	out, err := runInitializeCmd(t, initializeCmd, "--env-file="+db.envFile)
	require.NoError(t, err)
	assert.Contains(t, out, "administrator: admin <admin@example.com>")
	assert.Contains(t, out, "password: ",
		"the generated credential is labeled, so an unattended run reads it from the output")
	assert.Contains(t, out, "the generated credential — store it now; it is not recoverable")
	assert.Contains(t, out, "authorization: 52 created, 0 skipped",
		"the seed answers one summary line per seeder, not one line per record")
	assert.Equal(t, 1, strings.Count(out, "status:"),
		"the outcome is one status line — a second one is a second answer")

	// The system seed ran: the administrator role exists and the catalog is
	// populated beside it.
	pool := testPool(t, db.dsn)
	roleID, err := administratorRoleID(t.Context(), pool)
	require.NoError(t, err)
	assert.NotEmpty(t, roleID)

	permissionCount(t, pool)

	// The account exists with the generated credential, and the grant is
	// live.
	row := readAccountRow(t, pool, "admin@example.com")
	assert.Equal(t, "admin", row.username)
	assert.True(t, row.hasPassword)
	assert.True(t, row.isAdmin)
}

func TestInitializeHonorsExplicitCredentials(t *testing.T) {
	db := freshMigratedDatabase(t)

	out, err := runInitializeCmd(t, initializeCmd, "--env-file="+db.envFile,
		"--admin-email=ops@example.com", "--admin-username=ops_admin",
		"--admin-password=Expecto-Patronum-9")
	require.NoError(t, err)
	assert.NotContains(t, out, "the generated credential",
		"an explicit credential is never echoed back")

	row := readAccountRow(t, testPool(t, db.dsn), "ops@example.com")
	assert.Equal(t, "ops_admin", row.username)
	assert.True(t, row.isAdmin)
}

func TestInitializeRefusesAnInitializedDatabase(t *testing.T) {
	db := freshMigratedDatabase(t)
	_, err := runInitializeCmd(t, initializeCmd, "--env-file="+db.envFile)
	require.NoError(t, err)

	out, err := runInitializeCmd(t, initializeCmd, "--env-file="+db.envFile,
		"--admin-email=second@example.com")
	require.Error(t, err)
	assert.Contains(t, out, "status: refused",
		"the refusal is an outcome in the shape every outcome answers in")
	assert.Contains(t, out, "already holds 1 account")
	assert.Contains(t, out, "saka admin:reset-password")

	// The refusal is the whole of the run: no second account exists.
	row := readAccountRow(t, testPool(t, db.dsn), "second@example.com")
	assert.False(t, row.exists)
}

func TestInitializeRefusesAWeakCredential(t *testing.T) {
	db := freshMigratedDatabase(t)

	_, err := runInitializeCmd(t, initializeCmd, "--env-file="+db.envFile,
		"--admin-password=short")
	require.Error(t, err)
}

func TestAdminResetPasswordReplacesAnAdministratorsCredential(t *testing.T) {
	db := freshMigratedDatabase(t)
	_, err := runInitializeCmd(t, initializeCmd, "--env-file="+db.envFile)
	require.NoError(t, err)

	out, err := runInitializeCmd(t, adminResetPasswordCmd, "--env-file="+db.envFile,
		"admin@example.com", "--new-password=Gryffindor-2026!")
	require.NoError(t, err)
	assert.Contains(t, out, "status: password reset")

	// The new credential opens the account: the hash the row carries is the
	// one the presented password verifies against.
	hash := passwordHash(t, testPool(t, db.dsn), "admin@example.com")
	valid, err := crypto.NewPasswordHasher().Verify("Gryffindor-2026!", hash)
	require.NoError(t, err)
	assert.True(t, valid)
}

func TestAdminResetPasswordGeneratesARecoverableCredential(t *testing.T) {
	db := freshMigratedDatabase(t)
	_, err := runInitializeCmd(t, initializeCmd, "--env-file="+db.envFile)
	require.NoError(t, err)

	out, err := runInitializeCmd(t, adminResetPasswordCmd, "--env-file="+db.envFile, "admin@example.com")
	require.NoError(t, err)

	// The generated credential is printed once and is the one the row now
	// verifies against — the output is the only place it exists.
	lines := strings.Split(out, "\n")
	var generated string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 20 && !strings.Contains(line, " ") {
			generated = line
			break
		}
	}
	require.NotEmpty(t, generated, "the generated credential is in the output")

	hash := passwordHash(t, testPool(t, db.dsn), "admin@example.com")
	valid, err := crypto.NewPasswordHasher().Verify(generated, hash)
	require.NoError(t, err)
	assert.True(t, valid)
}

func TestAdminResetPasswordRefusesTheNonAdminAndTheUnknown(t *testing.T) {
	db := freshMigratedDatabase(t)
	_, err := runInitializeCmd(t, initializeCmd, "--env-file="+db.envFile)
	require.NoError(t, err)

	pool := testPool(t, db.dsn)
	seedRegularAccount(t, pool)

	_, err = runInitializeCmd(t, adminResetPasswordCmd, "--env-file="+db.envFile,
		"hermione@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not hold the "+authz.AdministratorRole+" role",
		"a regular account's recovery travels the forgotten-password flow")

	_, err = runInitializeCmd(t, adminResetPasswordCmd, "--env-file="+db.envFile,
		"nobody@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no account with address")
}

func TestAdminResetPasswordRefusesADisabledAdministrator(t *testing.T) {
	db := freshMigratedDatabase(t)
	_, err := runInitializeCmd(t, initializeCmd, "--env-file="+db.envFile)
	require.NoError(t, err)

	pool := testPool(t, db.dsn)
	_, err = pool.Exec(t.Context(), "UPDATE public.users SET disabled = TRUE WHERE email = 'admin@example.com'")
	require.NoError(t, err)

	_, err = runInitializeCmd(t, adminResetPasswordCmd, "--env-file="+db.envFile, "admin@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

// accountRow reads the one account the address names, with the facts the
// assertions name.
type accountRow struct {
	exists      bool
	username    string
	hasPassword bool
	isAdmin     bool
}

func readAccountRow(t *testing.T, pool *datastore.Postgres, email string) accountRow {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("u.id", "u.username", "p.password_hash IS NOT NULL")
	sb.From("public.users AS u")
	sb.JoinWithOption(sqlbuilder.LeftJoin, "public.user_passwords AS p", "p.user_id = u.id")
	sb.Where(sb.Equal("u.email", email))

	query, args := sb.Build()
	var id, username string
	var hasPassword bool
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&id, &username, &hasPassword); err != nil {
		return accountRow{}
	}

	return accountRow{exists: true, username: username, hasPassword: hasPassword, isAdmin: isAdmin(t, pool, id)}
}

func isAdmin(t *testing.T, pool *datastore.Postgres, userID string) bool {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(entity.TableUserRoles + " ur")
	sb.JoinWithOption(sqlbuilder.InnerJoin, entity.TableRoles+" r", "r.id = ur.role_id")
	sb.Where(sb.Equal("ur.user_id", userID), sb.Equal("r.slug", authz.AdministratorRole), sb.IsNull("ur.revoked_at"))

	query, args := sb.Build()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	return count > 0
}

func permissionCount(t *testing.T, pool *datastore.Postgres) {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(entity.TablePermissions)

	query, args := sb.Build()
	var count int
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&count))
	require.Greater(t, count, 0, "the catalog is populated")
}

func passwordHash(t *testing.T, pool *datastore.Postgres, email string) string {
	t.Helper()

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("p.password_hash")
	sb.From("public.user_passwords AS p")
	sb.JoinWithOption(sqlbuilder.InnerJoin, "public.users AS u", "u.id = p.user_id")
	sb.Where(sb.Equal("u.email", email))

	query, args := sb.Build()
	var hash string
	require.NoError(t, pool.QueryRow(t.Context(), query, args...).Scan(&hash))
	return hash
}

func seedRegularAccount(t *testing.T, pool *datastore.Postgres) {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO public.users (username, email, first_name, last_name, display_name)
		VALUES ('hermione_granger', 'hermione@example.com', 'Hermione', 'Granger', 'Hermione Granger')`)
	require.NoError(t, err)
}

func testPool(t *testing.T, dsn string) *datastore.Postgres {
	t.Helper()

	pool, err := datastore.NewPostgres(t.Context(), datastore.PostgresOptions{DSN: dsn})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Shutdown(context.Background()) })
	return pool
}
