package database

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	kgzip "github.com/klauspost/compress/gzip"
	kzip "github.com/klauspost/compress/zip"
	kzlib "github.com/klauspost/compress/zlib"
)

// Compression names the container a dump is stored in. A compressed dump is the
// same SQL; the container only changes how the bytes sit on disk.
//
// The formats come from klauspost/compress, which is already in the module graph
// and gives all three from one dependency.
type Compression string

const (
	// CompressionNone writes the dump as plain text.
	CompressionNone Compression = "none"
	// CompressionGzip writes it as a gzip stream (RFC 1952), the format
	// `gunzip` and every archive tool reads.
	CompressionGzip Compression = "gzip"
	// CompressionZlib writes it as a zlib stream (RFC 1950): the same deflate
	// data as gzip with a smaller, checksum-only wrapper.
	CompressionZlib Compression = "zlib"
	// CompressionZip writes it as a zip archive holding one entry.
	CompressionZip Compression = "zip"
)

// Compressions lists the supported values in the order the help text documents
// them.
var Compressions = []Compression{CompressionNone, CompressionGzip, CompressionZlib, CompressionZip}

// ErrUnsupportedCompression is returned for a file whose container is recognized
// but not one this tool writes. Naming the format is the point: the user picked
// the wrong file, and saying so beats calling it corrupt.
var ErrUnsupportedCompression = errors.New("database: unsupported compression format")

// ParseCompression reads a --compression value. An empty value means none, so a
// caller that did not pass the flag gets the plain dump.
func ParseCompression(value string) (Compression, error) {
	switch candidate := Compression(strings.ToLower(strings.TrimSpace(value))); candidate {
	case "", CompressionNone:
		return CompressionNone, nil
	case CompressionGzip, CompressionZlib, CompressionZip:
		return candidate, nil
	default:
		return "", fmt.Errorf("database: unknown compression %q; supported: %s",
			value, compressionList())
	}
}

// compressionList renders the supported values for an error or a usage line.
func compressionList() string {
	names := make([]string, len(Compressions))
	for i, c := range Compressions {
		names[i] = string(c)
	}
	return strings.Join(names, ", ")
}

// Ext is the suffix a compressed dump carries after the ".sql". It is empty for
// none, so a plain dump keeps the name it always had.
func (c Compression) Ext() string {
	switch c {
	case CompressionGzip:
		return ".gz"
	case CompressionZlib:
		return ".zz"
	case CompressionZip:
		return ".zip"
	default:
		return ""
	}
}

// IsCompressed reports whether the container changes the bytes on disk.
func (c Compression) IsCompressed() bool {
	return c == CompressionGzip || c == CompressionZlib || c == CompressionZip
}

// The first bytes of each container. Detection reads these rather than the file
// name, so a renamed dump still loads and a name that lies is not believed.
var (
	magicZip  = []byte{'P', 'K'}
	magicGzip = []byte{0x1f, 0x8b}
)

// unsupportedContainer names a format that is recognized so a refusal can say
// which one it is. A file this tool did not write is the common case, and
// "bzip2 is not supported" tells the user more than "not a dump".
type unsupportedContainer struct {
	magic []byte
	name  string
}

var unsupportedContainers = []unsupportedContainer{
	{[]byte{'B', 'Z', 'h'}, "bzip2"},
	{[]byte{0x28, 0xb5, 0x2f, 0xfd}, "zstd"},
	{[]byte{0xfd, '7', 'z', 'X', 'Z', 0x00}, "xz"},
	{[]byte{0x37, 0x7a, 0xbc, 0xaf, 0x27, 0x1c}, "lzma"},
	{[]byte{0x04, 0x22, 0x4d, 0x18}, "lz4"},
	{[]byte{0x1f, 0x9d}, "compress"},
	{[]byte{0x1f, 0xa0}, "compress"},
}

// detectCompression decides which container a dump carries from its first bytes.
//
// Anything that is not a recognized container is treated as plain SQL, which is
// then held to the dump's own contract: a file with no COPY block is refused
// later by the loader. A container that is recognized but unsupported is refused
// here, by name.
func detectCompression(head []byte) (Compression, error) {
	if len(head) == 0 {
		return CompressionNone, nil
	}

	if bytes.HasPrefix(head, magicZip) {
		return CompressionZip, nil
	}
	if bytes.HasPrefix(head, magicGzip) {
		return CompressionGzip, nil
	}
	// A zlib stream starts with a deflate method byte and a flag byte chosen so
	// that the pair is a multiple of 31. Both halves must hold, which is what
	// keeps ordinary text from being mistaken for zlib.
	if isZlibHeader(head) {
		return CompressionZlib, nil
	}

	for _, container := range unsupportedContainers {
		if bytes.HasPrefix(head, container.magic) {
			return "", fmt.Errorf("%w: %s", ErrUnsupportedCompression, container.name)
		}
	}
	return CompressionNone, nil
}

// isZlibHeader reports whether the first two bytes are a valid zlib header.
func isZlibHeader(head []byte) bool {
	if len(head) < 2 {
		return false
	}
	// The low nibble of the first byte is the compression method: 8 is deflate,
	// the only one zlib defines.
	if head[0]&0x0f != 8 {
		return false
	}
	return (uint16(head[0])<<8|uint16(head[1]))%31 == 0
}

// OpenDump opens a dump and wraps it in the reader its container needs, so the
// caller reads plain SQL whichever way the file was stored.
//
// The format is detected from the bytes, never from the name. A dump that was
// renamed still loads; a name that claims a container the bytes do not carry is
// not believed.
//
// The caller owns the returned reader and must close it.
func OpenDump(path string) (io.ReadCloser, Compression, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("database: open backup file: %w", err)
	}

	format, err := detectFile(file)
	if err != nil {
		_ = file.Close()
		return nil, "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, "", fmt.Errorf("database: rewind backup file: %w", err)
	}

	switch format {
	case CompressionNone:
		return file, format, nil

	case CompressionGzip:
		reader, err := kgzip.NewReader(file)
		if err != nil {
			_ = file.Close()
			return nil, "", fmt.Errorf("database: read gzip backup: %w", err)
		}
		return &compoundCloser{reader: reader, file: file}, format, nil

	case CompressionZlib:
		reader, err := kzlib.NewReader(file)
		if err != nil {
			_ = file.Close()
			return nil, "", fmt.Errorf("database: read zlib backup: %w", err)
		}
		return &compoundCloser{reader: reader, file: file}, format, nil

	case CompressionZip:
		return openZipDump(file)
	}

	_ = file.Close()
	return nil, "", fmt.Errorf("database: cannot read backup in %s format", format)
}

// openZipDump opens the single entry of a zip dump. A zip carries a directory of
// entries, so the archive is checked to hold exactly one: a dump is one file, and
// an archive of several has no defined meaning to load.
func openZipDump(file *os.File) (io.ReadCloser, Compression, error) {
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, "", fmt.Errorf("database: stat backup file: %w", err)
	}

	reader, err := kzip.NewReader(file, info.Size())
	if err != nil {
		_ = file.Close()
		return nil, "", fmt.Errorf("database: read zip backup: %w", err)
	}
	if len(reader.File) != 1 {
		_ = file.Close()
		return nil, "", fmt.Errorf("database: zip backup must hold exactly one file, found %d",
			len(reader.File))
	}

	entry, err := reader.File[0].Open()
	if err != nil {
		_ = file.Close()
		return nil, "", fmt.Errorf("database: open zip entry: %w", err)
	}
	return &compoundCloser{reader: entry, file: file}, CompressionZip, nil
}

// detectFile reads the leading bytes of a file and leaves it rewound.
func detectFile(file *os.File) (Compression, error) {
	head := make([]byte, 8)
	count, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("database: read backup file: %w", err)
	}
	return detectCompression(head[:count])
}

// compoundCloser pairs a decompressor with the file under it, so a caller reads
// plain SQL and releases one handle.
type compoundCloser struct {
	reader io.ReadCloser
	file   *os.File
}

// Read reads the decompressed SQL.
func (c *compoundCloser) Read(p []byte) (int, error) { return c.reader.Read(p) }

// Close releases the decompressor first, then the file, reporting the first
// failure so a caller cannot miss one.
func (c *compoundCloser) Close() error {
	err := c.reader.Close()
	if closeErr := c.file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// newCompressor wraps w in the writer of the container. entryName is the name
// the SQL file carries inside a container that stores one, which is zip.
func newCompressor(format Compression, w io.Writer, entryName string) (io.WriteCloser, error) {
	switch format {
	case CompressionGzip:
		return kgzip.NewWriter(w), nil
	case CompressionZlib:
		return kzlib.NewWriter(w), nil
	case CompressionZip:
		writer := kzip.NewWriter(w)
		entry, err := writer.CreateHeader(&kzip.FileHeader{
			Name:     entryName,
			Method:   kzip.Deflate,
			Modified: time.Now().UTC(),
		})
		if err != nil {
			return nil, fmt.Errorf("database: create zip entry: %w", err)
		}
		return &zipEntry{entry: entry, archive: writer}, nil
	}
	return nil, fmt.Errorf("database: cannot write a %s dump", format)
}

// zipEntry writes one entry of a zip archive.
//
// The archive owns the central directory, so the entry cannot be closed on its
// own: closing the archive is what finishes the entry. The two are one
// WriteCloser here so a caller cannot close the wrong one.
type zipEntry struct {
	entry   io.Writer
	archive *kzip.Writer
}

// Write appends to the entry.
func (z *zipEntry) Write(p []byte) (int, error) { return z.entry.Write(p) }

// Close finishes the entry by closing the archive around it.
func (z *zipEntry) Close() error { return z.archive.Close() }

// CompressFile wraps source in format and writes it to destination.
//
// The dump is written plain first and compressed after it is complete, which is
// what db:export does. Compressing a finished file means a failure during the
// dump cannot leave a half-written archive that still looks valid to a reader,
// and the entry name inside a zip is known before the archive is opened.
func CompressFile(source, destination string, format Compression, entryName string) error {
	if !format.IsCompressed() {
		return nil
	}

	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("database: open dump for compression: %w", err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(destination)
	if err != nil {
		return fmt.Errorf("database: create compressed dump: %w", err)
	}
	defer func() { _ = out.Close() }()

	compressor, err := newCompressor(format, out, entryName)
	if err != nil {
		return err
	}
	if _, err := io.Copy(compressor, in); err != nil {
		return fmt.Errorf("database: compress dump: %w", err)
	}
	// The compressor must be closed before the file is: that is what flushes the
	// last block and, for a zip, writes the central directory. Reading the output
	// before this point would give a truncated file.
	if err := compressor.Close(); err != nil {
		return fmt.Errorf("database: finish compressed dump: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("database: write compressed dump: %w", err)
	}
	return nil
}

// InspectDump counts what a dump carries without touching a database, so a dry
// run can report the work a restore would do.
func InspectDump(rd io.Reader) (DumpStats, error) {
	var stats DumpStats

	blocks, err := readBlocks(rd)
	if err != nil {
		return stats, err
	}
	if len(blocks) == 0 {
		return stats, ErrNoCopyBlocks
	}

	stats.Tables = len(blocks)
	for _, block := range blocks {
		// Every row ends with a newline in the text COPY format, and a newline
		// inside a value is escaped by the server, so counting newlines counts
		// rows exactly.
		stats.Rows += int64(bytes.Count(block.data, []byte("\n")))
	}
	return stats, nil
}
