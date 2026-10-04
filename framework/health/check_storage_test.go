package health_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/framework/health"
)

// storageResult runs the data directory check on dir and returns its detail.
func storageResult(t *testing.T, dir string) health.CheckResult {
	t.Helper()

	result := health.NewChecker(health.WithCheck(health.StorageCheck(dir))).Check(t.Context())
	detail, ok := result.Details[health.CheckNameStorage]
	require.True(t, ok, "result must carry the storage check")
	return detail
}

func TestStorageCheckPassesOnUsableDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o750))

	detail := storageResult(t, dir)

	assert.Equal(t, health.StatusUp, detail.Status)
	assert.Empty(t, detail.Error)
	assert.Equal(t, health.CheckNameStorage, detail.Name)
}

// A missing directory is a failure: the application would only discover it when
// it tried to store the first file.
func TestStorageCheckFailsWhenDirectoryMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nope")

	detail := storageResult(t, dir)

	assert.Equal(t, health.StatusDown, detail.Status)
	assert.Contains(t, detail.Error, "does not exist")
}

// A path that is a file, not a directory, must be reported as such rather than
// as a permission problem.
func TestStorageCheckFailsWhenPathIsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "afile")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))

	detail := storageResult(t, path)

	assert.Equal(t, health.StatusDown, detail.Status)
	assert.Contains(t, detail.Error, "not a directory")
}

// A directory that is not writable is a failure. The check writes a real file,
// because mode bits alone do not prove a write will succeed.
func TestStorageCheckFailsWhenDirectoryIsNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions, so the write probe would succeed")
	}

	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	detail := storageResult(t, dir)

	assert.Equal(t, health.StatusDown, detail.Status)
	assert.Contains(t, detail.Error, "not writable")
}

// A world-writable directory is a failure, not a note: it holds uploads and
// certificates, so any local user could replace them.
func TestStorageCheckFailsWhenDirectoryIsWorldWritable(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o777))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	detail := storageResult(t, dir)

	assert.Equal(t, health.StatusDown, detail.Status)
	assert.Contains(t, detail.Error, "world-writable")
	assert.Contains(t, detail.Error, "0777")
}

// The write probe must clean up after itself on success, or every health check
// would leave a file behind.
func TestStorageCheckLeavesNoFileBehind(t *testing.T) {
	dir := t.TempDir()

	detail := storageResult(t, dir)
	require.Equal(t, health.StatusUp, detail.Status)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "the write probe must be removed")
}

// The check is required, so an unusable data directory fails the whole probe.
func TestStorageCheckIsRequired(t *testing.T) {
	check := health.StorageCheck(t.TempDir())

	assert.False(t, check.Optional)
	assert.Positive(t, check.Timeout)
	assert.LessOrEqual(t, check.Timeout, health.DefaultTimeout)
}

// The report must not carry the directory path: the endpoint publishes this
// result, and a filesystem layout is not something an unauthenticated reader
// should learn.
func TestStorageCheckHidesTargetPath(t *testing.T) {
	dir := t.TempDir()
	relative := relativeToWorkingDir(t, dir)

	check := health.StorageCheck(relative)
	assert.Empty(t, check.Target)
}

// The CLI report is read by an operator who owns the machine, so its variant
// names the directory: absolute, so the report says which directory was
// inspected rather than leaving it relative to an unknown working directory.
func TestStorageCheckWithTargetReportsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	relative := relativeToWorkingDir(t, dir)

	check := health.StorageCheckWithTarget(relative)
	assert.True(t, filepath.IsAbs(check.Target), "target must be absolute: %s", check.Target)
	assert.Equal(t, dir, check.Target)
}

// A path must not leak into the error message either: the failure names the
// problem, the report stays free of the filesystem layout.
func TestStorageCheckErrorNamesNoPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	relative := relativeToWorkingDir(t, missing)

	detail := storageResult(t, relative)

	assert.Equal(t, health.StatusDown, detail.Status)
	assert.NotContains(t, detail.Error, missing)
	assert.NotContains(t, detail.Error, "nope")
}

// relativeToWorkingDir returns path relative to the test working directory, so
// the check receives a relative input without the test leaving its directory.
func relativeToWorkingDir(t *testing.T, path string) string {
	t.Helper()

	workingDir, err := os.Getwd()
	require.NoError(t, err)

	relative, err := filepath.Rel(workingDir, path)
	require.NoError(t, err)
	require.False(t, filepath.IsAbs(relative), "the fixture must be relative")
	return relative
}

// The check must work against the directory a data-directory convention
// names — "storage", the default `internal/config` resolves for
// `storage.local_path` — so a broken default is caught here rather than at
// deploy time. The literal keeps the framework free of the app's config
// import; the value's home and its sync with the CLI are the app's tests.
func TestStorageCheckOnRepositoryDataDir(t *testing.T) {
	detail := storageResult(t, repoPath(t, "storage"))

	assert.Equal(t, health.StatusUp, detail.Status, detail.Error)
}

// repoPath resolves a module-root-relative path from a test's working
// directory, which is the package directory.
func repoPath(t *testing.T, rel string) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, rel)
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "go.mod not found above %s", rel)
		dir = parent
	}
}
