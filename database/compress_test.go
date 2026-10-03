package database_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/database"
)

// Every supported format round-trips: the file it writes reads back as the same
// SQL, so the container changes nothing a loader has to know about.
func TestCompressFileRoundTrips(t *testing.T) {
	content := dump(t, dumper(t, migratedPoolDSN(t)), database.DumpOptions{DataOnly: true})

	for _, format := range database.Compressions {
		t.Run(string(format), func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "dump.sql")
			require.NoError(t, os.WriteFile(source, []byte(content), 0o644))

			if !format.IsCompressed() {
				assert.NoError(t, database.CompressFile(source, source+format.Ext(), format, "dump.sql"))
				assert.FileExists(t, source, "an uncompressed dump must be left alone")
				return
			}

			target := source + format.Ext()
			require.NoError(t, database.CompressFile(source, target, format, "dump.sql"))
			assert.FileExists(t, target)

			reader, detected, err := database.OpenDump(target)
			require.NoError(t, err)
			defer func() { _ = reader.Close() }()

			assert.Equal(t, format, detected, "the format must be detected from the bytes")
			got, err := io.ReadAll(reader)
			require.NoError(t, err)
			assert.Equal(t, content, string(got), "the decompressed dump must match the original byte for byte")
		})
	}
}

// The container is decided by the bytes, never by the name. A dump that was
// renamed still loads, and a name that claims a format the bytes do not carry is
// not believed.
func TestOpenDumpDetectsByContentNotName(t *testing.T) {
	content := dump(t, dumper(t, migratedPoolDSN(t)), database.DumpOptions{DataOnly: true})

	dir := t.TempDir()
	source := filepath.Join(dir, "dump.sql")
	require.NoError(t, os.WriteFile(source, []byte(content), 0o644))

	// A gzip stream under a plain .sql name.
	gzipPath := filepath.Join(dir, "lying.sql")
	require.NoError(t, database.CompressFile(source, gzipPath, database.CompressionGzip, "dump.sql"))

	reader, format, err := database.OpenDump(gzipPath)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	assert.Equal(t, database.CompressionGzip, format)

	// Plain SQL under a name that claims gzip.
	plainPath := filepath.Join(dir, "honest.sql.gz")
	require.NoError(t, os.WriteFile(plainPath, []byte(content), 0o644))

	reader, format, err = database.OpenDump(plainPath)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	assert.Equal(t, database.CompressionNone, format, "a name is not evidence of a format")
}

// A container this tool does not write is refused by name. The user picked the
// wrong file, and saying which format it is beats calling it corrupt.
func TestOpenDumpRejectsUnsupportedContainers(t *testing.T) {
	dir := t.TempDir()

	for _, tc := range []struct {
		name  string
		magic []byte
		want  string
	}{
		{"bzip2", []byte{'B', 'Z', 'h', '9'}, "bzip2"},
		{"zstd", []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00}, "zstd"},
		{"xz", []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}, "xz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".bin")
			require.NoError(t, os.WriteFile(path, tc.magic, 0o644))

			_, _, err := database.OpenDump(path)
			require.ErrorIs(t, err, database.ErrUnsupportedCompression)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// A zip holds a directory of entries, so a dump is only well formed when it
// holds exactly one. An archive of several has no defined meaning to load.
func TestOpenDumpRejectsMultiEntryZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multi.sql.zip")
	writeZipWithTwoEntries(t, path)

	_, _, err := database.OpenDump(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one file")
}

// An empty file is not a container and not a dump. It reads as plain SQL, which
// the loader then refuses for carrying no COPY block.
func TestOpenDumpTreatsEmptyFileAsPlainSQL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.sql")
	require.NoError(t, os.WriteFile(path, nil, 0o644))

	reader, format, err := database.OpenDump(path)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	assert.Equal(t, database.CompressionNone, format)

	_, err = database.InspectDump(reader)
	require.ErrorIs(t, err, database.ErrNoCopyBlocks)
}

// A dry run has to report the work without a database, so the inspection counts
// the blocks and rows a restore would load.
func TestInspectDumpCountsBlocksAndRows(t *testing.T) {
	content := dump(t, dumper(t, migratedPoolDSN(t)), database.DumpOptions{DataOnly: true})

	stats, err := database.InspectDump(strings.NewReader(content))
	require.NoError(t, err)

	// The count must agree with the dump the exporter reported.
	_, dsn := migratedPool(t)
	restoreStats, err := database.NewRestorer(dumper(t, dsn), false).
		Restore(t.Context(), strings.NewReader(content), nil)
	require.NoError(t, err)

	assert.Equal(t, restoreStats.Tables, stats.Tables)
	assert.Equal(t, restoreStats.Rows, stats.Rows)
}

// InspectDump reads a compressed dump through the same path a restore does, so a
// dry run and a real run cannot disagree about the size of the work.
func TestInspectDumpReadsACompressedFile(t *testing.T) {
	content := dump(t, dumper(t, migratedPoolDSN(t)), database.DumpOptions{DataOnly: true})

	dir := t.TempDir()
	source := filepath.Join(dir, "dump.sql")
	require.NoError(t, os.WriteFile(source, []byte(content), 0o644))

	target := filepath.Join(dir, "dump.sql.gz")
	require.NoError(t, database.CompressFile(source, target, database.CompressionGzip, "dump.sql"))

	reader, format, err := database.OpenDump(target)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	require.Equal(t, database.CompressionGzip, format)

	compressed, err := database.InspectDump(reader)
	require.NoError(t, err)

	plain, err := database.InspectDump(strings.NewReader(content))
	require.NoError(t, err)
	assert.Equal(t, plain, compressed)
}

// ParseCompression is the seam between the flag and the format. An empty value
// means none, the comparison ignores case and padding, and anything else names
// the values that would have worked.
func TestParseCompression(t *testing.T) {
	for input, want := range map[string]database.Compression{
		"":       database.CompressionNone,
		"none":   database.CompressionNone,
		"gzip":   database.CompressionGzip,
		"GZIP":   database.CompressionGzip,
		"  Zip ": database.CompressionZip,
		"zlib":   database.CompressionZlib,
	} {
		got, err := database.ParseCompression(input)
		require.NoError(t, err, "input %q", input)
		assert.Equal(t, want, got, "input %q", input)
	}

	for _, bad := range []string{"tar", "bzip2", "gzip2", "zstd"} {
		_, err := database.ParseCompression(bad)
		require.Error(t, err, "input %q", bad)
		assert.Contains(t, err.Error(), "supported:", "the error must list the values that work")
	}
}

// A compressed dump is smaller than the plain one, which is the only reason to
// ask for it.
func TestCompressionShrinksTheDump(t *testing.T) {
	// Repetitive content stands in for a table with many similar rows.
	content := strings.Repeat(`COPY "public"."users"("id", "email") FROM stdin WITH (FORMAT text);`+"\n"+
		"1\talice@example.com\n2\tbob@example.com\n\\.\n", 200)

	dir := t.TempDir()
	source := filepath.Join(dir, "dump.sql")
	require.NoError(t, os.WriteFile(source, []byte(content), 0o644))

	for _, format := range []database.Compression{
		database.CompressionGzip, database.CompressionZlib, database.CompressionZip,
	} {
		t.Run(string(format), func(t *testing.T) {
			target := filepath.Join(dir, "dump.sql"+format.Ext())
			require.NoError(t, database.CompressFile(source, target, format, "dump.sql"))

			info, err := os.Stat(target)
			require.NoError(t, err)
			assert.Less(t, info.Size(), int64(len(content)), "the compressed dump must be smaller")
		})
	}
}

// Compressing the same dump twice produces the same bytes, so a compressed
// backup is as reproducible as the plain one it came from.
func TestCompressionIsDeterministic(t *testing.T) {
	content := strings.Repeat("1\talice@example.com\n2\tbob@example.com\n", 500)

	dir := t.TempDir()
	source := filepath.Join(dir, "dump.sql")
	require.NoError(t, os.WriteFile(source, []byte(content), 0o644))

	for _, format := range []database.Compression{
		database.CompressionGzip, database.CompressionZlib, database.CompressionZip,
	} {
		t.Run(string(format), func(t *testing.T) {
			first := filepath.Join(dir, "a"+format.Ext())
			second := filepath.Join(dir, "b"+format.Ext())

			require.NoError(t, database.CompressFile(source, first, format, "dump.sql"))
			require.NoError(t, database.CompressFile(source, second, format, "dump.sql"))

			a, err := os.ReadFile(first)
			require.NoError(t, err)
			b, err := os.ReadFile(second)
			require.NoError(t, err)

			// The zip entry records a modification time, so the archives differ
			// only in that field. Compare the payloads rather than the headers.
			if format == database.CompressionZip {
				assertZipPayloadEqual(t, a, b)
				return
			}
			assert.True(t, bytes.Equal(a, b), "two compressions of one dump must be byte-identical")
		})
	}
}

// Ext is what a caller appends to a dump name, so it must be empty for none and
// distinct for every format.
func TestCompressionExt(t *testing.T) {
	assert.Empty(t, database.CompressionNone.Ext())
	assert.Equal(t, ".gz", database.CompressionGzip.Ext())
	assert.Equal(t, ".zz", database.CompressionZlib.Ext())
	assert.Equal(t, ".zip", database.CompressionZip.Ext())

	assert.False(t, database.CompressionNone.IsCompressed())
	assert.True(t, database.CompressionGzip.IsCompressed())
	assert.True(t, database.CompressionZlib.IsCompressed())
	assert.True(t, database.CompressionZip.IsCompressed())
}
