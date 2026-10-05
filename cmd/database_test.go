//go:build debug

package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/dustin/go-humanize"

	"github.com/riipandi/saka/internal/database"
)

// runDBExportCmd runs db:export with the given stdin and arguments.
func runDBExportCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runDBCmd(t, dbExportCmd, "", args...)
}

// runDBExportCmdIn runs db:export against an explicit data directory.
func runDBExportCmdIn(t *testing.T, dataDir string, args ...string) (string, error) {
	t.Helper()
	return runDBCmdIn(t, dataDir, dbExportCmd, "", args...)
}

// runDBImportCmd runs db:import with the given stdin and arguments.
func runDBImportCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	return runDBCmd(t, dbImportCmd, stdin, args...)
}

// runDBCmd runs one database command against a throwaway data directory, so a
// test never writes into the checkout.
func runDBCmd(t *testing.T, command *cli.Command, stdin string, args ...string) (string, error) {
	t.Helper()
	return runDBCmdIn(t, t.TempDir(), command, stdin, args...)
}

// runDBCmdIn runs one database command with an explicit data directory, written
// into the config file. There is no --data-dir flag: the file is the only place
// the directory is set.
func runDBCmdIn(
	t *testing.T,
	dataDir string,
	command *cli.Command,
	stdin string,
	args ...string,
) (string, error) {
	t.Helper()

	configFor(t, dataDir)

	var out bytes.Buffer
	root := testRoot(&out, stdin, dbExportCmd, dbImportCmd)
	err := root.Run(t.Context(), append([]string{"saka", command.Name}, args...))
	return out.String(), err
}

// exportTo writes a dump to an explicit path and returns it.
func exportTo(t *testing.T, envFile, path string, extra ...string) {
	t.Helper()
	args := append([]string{"--env-file=" + envFile, "--output=" + path}, extra...)
	_, err := runDBExportCmd(t, args...)
	require.NoError(t, err)
}

// A dump lands in the data directory when --output is not given, and the
// directory is created on the way.
func TestDBExportWritesToTheDataDirectory(t *testing.T) {
	envFile := migratedDatabase(t)
	dataDir := filepath.Join(t.TempDir(), "storage")

	out, err := runDBExportCmdIn(t, dataDir, "--env-file="+envFile)
	require.NoError(t, err)

	// The report names the database it read and the file it wrote, as one
	// aligned block with no blank line between the two.
	assert.Contains(t, out, "database:  ")
	assert.Contains(t, out, "written:   ")
	assert.Contains(t, out, dataDir)
	assert.NotContains(t, out, "\n\n")

	entries, err := os.ReadDir(filepath.Join(dataDir, backupDir))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, strings.HasSuffix(entries[0].Name(), ".sql"))
	assert.Regexp(t, `^saka-\d{8}_\d{4}\.sql$`, entries[0].Name())
}

// The generated name is readable to the minute, so two exports inside one minute
// land on the same path. The first dump must not be lost silently.
func TestDBExportRefusesToOverwriteAGeneratedDump(t *testing.T) {
	envFile := migratedDatabase(t)
	dataDir := filepath.Join(t.TempDir(), "storage")

	_, err := runDBExportCmdIn(t, dataDir, "--env-file="+envFile)
	require.NoError(t, err)

	_, err = runDBExportCmdIn(t, dataDir, "--env-file="+envFile)
	require.ErrorIs(t, err, ErrDumpFileExists)

	// --overwrite is the way to ask for the replacement.
	_, err = runDBExportCmdIn(t, dataDir, "--env-file="+envFile, "--overwrite")
	require.NoError(t, err)
}

// --output writes exactly where it is told.
func TestDBExportHonorsOutput(t *testing.T) {
	envFile := migratedDatabase(t)
	path := filepath.Join(t.TempDir(), "dump.sql")

	out, err := runDBExportCmd(t, "--env-file="+envFile, "--output="+path)
	require.NoError(t, err)
	assert.Contains(t, out, path)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(content), "-- saka database dump")
}

// A missing parent directory must fail rather than be created: the path came
// from the user, so a typo has to be visible.
func TestDBExportRefusesMissingOutputDirectory(t *testing.T) {
	envFile := migratedDatabase(t)

	_, err := runDBExportCmd(t, "--env-file="+envFile,
		"--output="+filepath.Join(t.TempDir(), "missing", "dump.sql"))
	require.Error(t, err)
}

// --schema-only and --data-only describe opposite dumps, so asking for both is a
// mistake rather than a preference.
func TestDBExportRejectsConflictingSelection(t *testing.T) {
	envFile := migratedDatabase(t)

	_, err := runDBExportCmd(t, "--env-file="+envFile, "--schema-only", "--data-only",
		"--output="+filepath.Join(t.TempDir(), "dump.sql"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

// The schema dump carries DDL, the data dump carries rows, and neither carries
// the other.
func TestDBExportSelection(t *testing.T) {
	envFile := migratedDatabase(t)

	schemaPath := filepath.Join(t.TempDir(), "schema.sql")
	exportTo(t, envFile, schemaPath, "--schema-only")
	schemaDump, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	assert.Contains(t, string(schemaDump), "CREATE TABLE")
	assert.NotContains(t, string(schemaDump), "FROM stdin")

	dataPath := filepath.Join(t.TempDir(), "data.sql")
	exportTo(t, envFile, dataPath, "--data-only")
	dataDump, err := os.ReadFile(dataPath)
	require.NoError(t, err)
	assert.Contains(t, string(dataDump), "FROM stdin")
	assert.NotContains(t, string(dataDump), "CREATE TABLE")
}

// A dump exported from one database loads into another, which is the whole point
// of the pair.
func TestDBImportRoundTrips(t *testing.T) {
	sourceEnv := migratedDatabase(t)
	_, err := runMigrateSeedCmd(t, "", "--env-file="+sourceEnv, "--force")
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "dump.sql")
	exportTo(t, sourceEnv, path, "--data-only")

	targetEnv := migratedDatabase(t)
	out, err := runDBImportCmd(t, "", "--env-file="+targetEnv, "--truncate", "--force", path)
	require.NoError(t, err)

	assert.Contains(t, out, "loaded:    ")
	assert.Contains(t, out, path)
	assert.Equal(t, 6, countUsers(t, targetEnv))
}

// Without a file there is nothing to import, so the argument is required.
func TestDBImportRequiresAFile(t *testing.T) {
	envFile := migratedDatabase(t)

	_, err := runDBImportCmd(t, "", "--env-file="+envFile)
	require.Error(t, err)
}

// A file that is not a dump must be reported as such rather than loaded as
// nothing.
func TestDBImportRejectsForeignFile(t *testing.T) {
	envFile := migratedDatabase(t)
	path := filepath.Join(t.TempDir(), "not-a-dump.sql")
	require.NoError(t, os.WriteFile(path, []byte("-- nothing to see\n"), 0o644))

	_, err := runDBImportCmd(t, "", "--env-file="+envFile, path)
	require.ErrorIs(t, err, database.ErrNoCopyBlocks)
}

// A missing file must fail before anything is touched.
func TestDBImportRejectsMissingFile(t *testing.T) {
	envFile := migratedDatabase(t)

	_, err := runDBImportCmd(t, "", "--env-file="+envFile,
		filepath.Join(t.TempDir(), "absent.sql"))
	require.Error(t, err)
}

// --truncate is destructive, so a declined prompt must leave the database alone.
func TestDBImportTruncateDeclined(t *testing.T) {
	sourceEnv := migratedDatabase(t)
	_, err := runMigrateSeedCmd(t, "", "--env-file="+sourceEnv, "--force")
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "dump.sql")
	exportTo(t, sourceEnv, path, "--data-only")

	targetEnv := migratedDatabase(t)

	previous := terminalCheck
	terminalCheck = func(*cli.Command) bool { return true }
	t.Cleanup(func() { terminalCheck = previous })

	out, err := runDBImportCmd(t, "n\n", "--env-file="+targetEnv, "--truncate", path)
	require.NoError(t, err)

	assert.Contains(t, out, "import cancelled")
	assert.Zero(t, countUsers(t, targetEnv))
}

// The backup directory may not exist yet, on a fresh checkout or after a
// cleanup. A generated path creates it, and --overwrite must not change that: a
// first run that happens to pass --overwrite has nothing to overwrite.
func TestDBExportCreatesTheBackupDirectory(t *testing.T) {
	envFile := migratedDatabase(t)

	for _, extra := range [][]string{nil, {"--overwrite"}, {"--compression=gzip"}, {"--overwrite", "--compression=zip"}} {
		t.Run(strings.Join(extra, "+"), func(t *testing.T) {
			dataDir := filepath.Join(t.TempDir(), "storage")

			args := append([]string{"--env-file=" + envFile, "--data-only"}, extra...)
			_, err := runDBExportCmdIn(t, dataDir, args...)
			require.NoError(t, err)

			entries, err := os.ReadDir(filepath.Join(dataDir, backupDir))
			require.NoError(t, err)
			assert.Len(t, entries, 1)
		})
	}
}

// A directory that cannot be created or written is reported, rather than
// failing somewhere further along with no explanation.
func TestDBExportReportsAnUnusableBackupDirectory(t *testing.T) {
	envFile := migratedDatabase(t)

	// A file where the directory should be.
	dataDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, backupDir), []byte("not a directory"), 0o644))

	_, err := runDBExportCmdIn(t, dataDir, "--env-file="+envFile, "--data-only")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backup directory")
}

// An --output path keeps its directory uncreated, so a typo stays visible rather
// than turning into a directory tree.
func TestDBExportDoesNotCreateAnOutputDirectory(t *testing.T) {
	envFile := migratedDatabase(t)
	path := filepath.Join(t.TempDir(), "missing", "dump.sql")

	_, err := runDBExportCmd(t, "--env-file="+envFile, "--data-only", "--output="+path)
	require.Error(t, err)
	assert.NoFileExists(t, path)
}

// The report names the file it wrote and how big it is, which is the answer a
// reader looks for after an export.
func TestDBExportReportsTheFileSize(t *testing.T) {
	envFile := migratedDatabase(t)
	path := filepath.Join(t.TempDir(), "dump.sql")

	out, err := runDBExportCmd(t, "--env-file="+envFile, "--data-only", "--output="+path)
	require.NoError(t, err)

	assert.Contains(t, out, "size:")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Contains(t, out, humanize.Bytes(uint64(info.Size())), "the size must be the real one")
}

// A compressed dump reports the size of the compressed file, not the plain one
// it was made from.
func TestDBExportReportsTheCompressedSize(t *testing.T) {
	envFile := migratedDatabase(t)
	path := filepath.Join(t.TempDir(), "dump.sql")

	out, err := runDBExportCmd(t, "--env-file="+envFile, "--data-only",
		"--compression=gzip", "--output="+path)
	require.NoError(t, err)

	compressed := path + ".gz"
	info, err := os.Stat(compressed)
	require.NoError(t, err)
	assert.Contains(t, out, humanize.Bytes(uint64(info.Size())))
}

// --force skips the prompt, which is what a script needs.
func TestDBImportTruncateForceSkipsPrompt(t *testing.T) {
	sourceEnv := migratedDatabase(t)
	_, err := runMigrateSeedCmd(t, "", "--env-file="+sourceEnv, "--force")
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "dump.sql")
	exportTo(t, sourceEnv, path, "--data-only")

	targetEnv := migratedDatabase(t)

	// The terminal check is armed to prompt, so only --force can get past it.
	previous := terminalCheck
	terminalCheck = func(*cli.Command) bool { return true }
	t.Cleanup(func() { terminalCheck = previous })

	out, err := runDBImportCmd(t, "", "--env-file="+targetEnv, "--truncate", "--force", path)
	require.NoError(t, err)

	assert.NotContains(t, out, "import cancelled")
	assert.Equal(t, 6, countUsers(t, targetEnv))
}

// Every compression format exports to the right name and imports back, which is
// the contract the pair exists for.
func TestDBExportImportCompressionRoundTrip(t *testing.T) {
	sourceEnv := migratedDatabase(t)
	_, err := runMigrateSeedCmd(t, "", "--env-file="+sourceEnv, "--force")
	require.NoError(t, err)

	for _, format := range []string{"none", "gzip", "zlib", "zip"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dump.sql")
			exportTo(t, sourceEnv, path, "--data-only", "--compression="+format)

			// The suffix is added to the name the user gave.
			written := path
			switch format {
			case "gzip":
				written += ".gz"
			case "zlib":
				written += ".zz"
			case "zip":
				written += ".zip"
			}
			assert.FileExists(t, written)
			if format != "none" {
				assert.NoFileExists(t, path, "the plain dump must not be left behind")
			}

			targetEnv := migratedDatabase(t)
			out, err := runDBImportCmd(t, "", "--env-file="+targetEnv, "--truncate", "--force", written)
			require.NoError(t, err)
			assert.Equal(t, 6, countUsers(t, targetEnv), "the rows must survive the container")

			if format != "none" {
				assert.Contains(t, out, "format:")
				assert.Contains(t, out, format)
			}
		})
	}
}

// A generated name carries the compression suffix exactly once.
func TestDBExportGeneratedNameCarriesTheSuffixOnce(t *testing.T) {
	envFile := migratedDatabase(t)
	dataDir := filepath.Join(t.TempDir(), "storage")

	out, err := runDBExportCmdIn(t, dataDir, "--env-file="+envFile, "--data-only",
		"--compression=gzip")
	require.NoError(t, err)

	entries, err := os.ReadDir(filepath.Join(dataDir, backupDir))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Regexp(t, `^saka-\d{8}_\d{4}\.sql\.gz$`, entries[0].Name(), "the suffix must appear once")
	assert.Contains(t, out, entries[0].Name())
}

// A dump is the whole database in one file, so it must not be readable by
// anyone but its owner — whichever path wrote it.
func TestDBExportWritesADumpOnlyItsOwnerCanRead(t *testing.T) {
	envFile := migratedDatabase(t)

	t.Run("generated name", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "storage")
		_, err := runDBExportCmdIn(t, dataDir, "--env-file="+envFile)
		require.NoError(t, err)

		entries, err := os.ReadDir(filepath.Join(dataDir, backupDir))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assertOwnerOnly(t, filepath.Join(dataDir, backupDir, entries[0].Name()))
	})

	t.Run("output path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "dump.sql")
		_, err := runDBExportCmd(t, "--env-file="+envFile, "--output="+path)
		require.NoError(t, err)
		assertOwnerOnly(t, path)
	})

	t.Run("overwrite", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "dump.sql")
		// A file that already exists with looser permissions: --overwrite must
		// not leave the dump readable just because the old file was.
		require.NoError(t, os.WriteFile(path, []byte("stale"), 0o644))

		_, err := runDBExportCmd(t, "--env-file="+envFile, "--output="+path, "--overwrite")
		require.NoError(t, err)
		assertOwnerOnly(t, path)
	})
}

// assertOwnerOnly reports the permission bits of a written dump: read and write
// for the owner, nothing for the group or anyone else.
func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o600), info.Mode().Perm(),
		"%s must be readable by its owner alone", path)
}

// A generated name that already exists is refused for the file the run would
// actually leave behind, which is the compressed one.
func TestDBExportRefusesToOverwriteACompressedGeneratedDump(t *testing.T) {
	envFile := migratedDatabase(t)
	dataDir := filepath.Join(t.TempDir(), "storage")

	_, err := runDBExportCmdIn(t, dataDir, "--env-file="+envFile, "--data-only",
		"--compression=gzip")
	require.NoError(t, err)

	_, err = runDBExportCmdIn(t, dataDir, "--env-file="+envFile, "--data-only",
		"--compression=gzip")
	require.ErrorIs(t, err, ErrDumpFileExists)

	_, err = runDBExportCmdIn(t, dataDir, "--env-file="+envFile, "--data-only",
		"--compression=gzip", "--overwrite")
	require.NoError(t, err)

	entries, err := os.ReadDir(filepath.Join(dataDir, backupDir))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "--overwrite replaces the file rather than adding one")
}

// A different format is a different file, so it does not collide with one
// already exported in another format.
func TestDBExportCompressionDoesNotCollideAcrossFormats(t *testing.T) {
	envFile := migratedDatabase(t)
	dataDir := filepath.Join(t.TempDir(), "storage")

	for _, format := range []string{"gzip", "zlib"} {
		_, err := runDBExportCmdIn(t, dataDir, "--env-file="+envFile, "--data-only",
			"--compression="+format)
		require.NoError(t, err, "format %s must not collide with the other", format)
	}

	entries, err := os.ReadDir(filepath.Join(dataDir, backupDir))
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

// An unknown value is refused before any work starts, and the error names what
// would have worked.
func TestDBExportRejectsUnknownCompression(t *testing.T) {
	envFile := migratedDatabase(t)

	_, err := runDBExportCmd(t, "--env-file="+envFile, "--data-only",
		"--compression=tar", "--output="+filepath.Join(t.TempDir(), "dump.sql"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "supported:")
	assert.Contains(t, err.Error(), "gzip")
}

// A dry run reports the work and changes nothing, and it needs no database at
// all: the point is to look before agreeing to a restore.
func TestDBImportDryRunChangesNothing(t *testing.T) {
	sourceEnv := migratedDatabase(t)
	_, err := runMigrateSeedCmd(t, "", "--env-file="+sourceEnv, "--force")
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "dump.sql")
	exportTo(t, sourceEnv, path, "--data-only")

	targetEnv := migratedDatabase(t)

	out, err := runDBImportCmd(t, "", "--env-file="+targetEnv, "--dry-run", path)
	require.NoError(t, err)

	assert.Contains(t, out, "loaded:")
	assert.Contains(t, out, "status: nothing loaded (dry run)")
	assert.Zero(t, countUsers(t, targetEnv), "a dry run must not write")
}

// A dry run must not need a reachable database, so it works with a DSN that
// points nowhere.
func TestDBImportDryRunNeedsNoDatabase(t *testing.T) {
	sourceEnv := migratedDatabase(t)
	path := filepath.Join(t.TempDir(), "dump.sql")
	exportTo(t, sourceEnv, path, "--data-only")

	ghostEnv := filepath.Join(t.TempDir(), "ghost.env")
	require.NoError(t, os.WriteFile(ghostEnv,
		[]byte("DATABASE_URL=postgresql://nobody@127.0.0.1:1/ghost?sslmode=disable\n"), 0o600))

	out, err := runDBImportCmd(t, "", "--env-file="+ghostEnv, "--dry-run", path)
	require.NoError(t, err)
	assert.Contains(t, out, "status: nothing loaded (dry run)")
}

// A format this tool does not write is refused by name, before anything is
// touched.
func TestDBImportRejectsUnsupportedFormat(t *testing.T) {
	envFile := migratedDatabase(t)
	path := filepath.Join(t.TempDir(), "foreign.sql.bz2")
	require.NoError(t, os.WriteFile(path, []byte{'B', 'Z', 'h', '9', 0x31}, 0o644))

	_, err := runDBImportCmd(t, "", "--env-file="+envFile, path)
	require.ErrorIs(t, err, database.ErrUnsupportedCompression)
	assert.Contains(t, err.Error(), "bzip2")
}

// A restore is destructive, so it asks even without --truncate. A declined
// prompt leaves the database alone.
func TestDBImportAsksBeforeRestoring(t *testing.T) {
	sourceEnv := migratedDatabase(t)
	_, err := runMigrateSeedCmd(t, "", "--env-file="+sourceEnv, "--force")
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "dump.sql")
	exportTo(t, sourceEnv, path, "--data-only")

	targetEnv := migratedDatabase(t)

	previous := terminalCheck
	terminalCheck = func(*cli.Command) bool { return true }
	t.Cleanup(func() { terminalCheck = previous })

	out, err := runDBImportCmd(t, "n\n", "--env-file="+targetEnv, path)
	require.NoError(t, err)

	assert.Contains(t, out, "import cancelled")
	assert.Zero(t, countUsers(t, targetEnv))
}
