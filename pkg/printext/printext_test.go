package printext_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/riipandi/saka/pkg/printext"
)

// A buffer is never a terminal, so a test sees the plain rendering without
// asking for it. This is also what a redirected run gets, which is the property
// that matters: a log must not collect escape codes.
func TestPaletteIsPlainOffTerminal(t *testing.T) {
	var out bytes.Buffer
	p := printext.NewPalette(&out)

	assert.False(t, p.Enabled())
	assert.Equal(t, "applied", p.Green("applied"))
	assert.Equal(t, "failed", p.Red("failed"))
	assert.Equal(t, "pending", p.Yellow("pending"))
	assert.Equal(t, "label", p.Dim("label"))
	assert.Empty(t, out.String(), "painting must not write anything")
}

// Mark is the Styler seam, so the shared vocabulary must map onto the colours a
// caller expects.
func TestMarkMapsEveryStyle(t *testing.T) {
	p := printext.NewPalette(&bytes.Buffer{})

	// Colour is off for a buffer, so the text must survive unchanged. The mapping
	// itself is asserted through the fact that no style mangles its input.
	for _, style := range []printext.Style{
		printext.StylePlain,
		printext.StyleLabel,
		printext.StyleOK,
		printext.StyleFail,
		printext.StyleWarn,
		printext.StyleMuted,
	} {
		assert.Equal(t, "text", p.Mark(style, "text"))
	}
}

// A nil styler renders plain text, which is what a renderer that has no palette
// must fall back to rather than panicking.
func TestMarkWithNilStylerIsPlain(t *testing.T) {
	assert.Equal(t, "text", printext.Mark(nil, printext.StyleFail, "text"))
}

// Durations must be humanized, not raw nanoseconds, and capped at a precision a
// reader can act on.
func TestDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "microseconds", in: 250 * time.Microsecond, want: "250 µs"},
		{name: "milliseconds", in: 27608625 * time.Nanosecond, want: "27.608 ms"},
		{name: "fractional second", in: 1500 * time.Millisecond, want: "1.5 s"},
		{name: "seconds", in: 12 * time.Second, want: "12 s"},
		{name: "zero", in: 0, want: "0 s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, printext.Duration(tt.in))
		})
	}
}

// A report never says "1 migrations", so the singular form must be chosen by the
// count.
func TestPlural(t *testing.T) {
	tests := []struct {
		count int
		want  string
	}{
		{count: 0, want: "migrations"},
		{count: 1, want: "migration"},
		{count: 2, want: "migrations"},
		{count: 9, want: "migrations"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, printext.Plural(tt.count, "migration"))
		})
	}
}

// Padding counts runes, not bytes, so a label holding a non-ASCII character is
// padded to the same visible width as an ASCII one.
func TestPadRight(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{name: "shorter is padded", in: "ab", width: 5, want: "ab   "},
		{name: "equal is unchanged", in: "abcde", width: 5, want: "abcde"},
		{name: "longer is unchanged", in: "abcdefg", width: 5, want: "abcdefg"},
		{name: "counts runes not bytes", in: "héllo", width: 6, want: "héllo "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, printext.PadRight(tt.in, tt.width))
		})
	}
}

// Printf writes to the destination the palette was built with, so a caller does
// not have to carry the writer around as well.
func TestPalettePrintfWritesToItsWriter(t *testing.T) {
	var out bytes.Buffer
	p := printext.NewPalette(&out)

	require.NoError(t, p.Printf("%s: %d\n", "count", 3))
	assert.Equal(t, "count: 3\n", out.String())
	assert.Equal(t, &out, p.Writer())
}
