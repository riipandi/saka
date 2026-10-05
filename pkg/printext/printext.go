// Package printext renders human-readable command output.
//
// It answers two questions that every command faces and that would otherwise be
// answered twice, in two places, and drift apart:
//
//   - Which colour does this piece of text get? The answer is a Style, which
//     names what the text means rather than what colour it is. A caller marks
//     its output with a Style; the palette decides the colour.
//   - How is this number written for a human? Durations and counts have one
//     rendering each, shared by every surface, so the CLI never says "27.608 ms"
//     where another command says "0s".
//
// The package owns presentation only. It never decides layout: how wide a column
// is, what a label says, and which lines a report holds are decisions of the
// caller.
package printext

import (
	"fmt"
	"io"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/fatih/color"
)

// Style names what a piece of text means, so a caller can mark it without
// choosing a colour. The meaning is what travels between packages; the colour is
// an implementation detail of Palette.
type Style int

const (
	// StylePlain is text with no meaning attached. It is the zero value, so an
	// unmarked part stays plain.
	StylePlain Style = iota
	// StyleLabel is the key of a "key: value" line, or any other text that names
	// what follows it.
	StyleLabel
	// StyleOK is work that succeeded, or a component that answered.
	StyleOK
	// StyleFail is work that failed, or a component that is down.
	StyleFail
	// StyleWarn is a state that is neither a pass nor a failure: something
	// pending, skipped, or rolled back.
	StyleWarn
	// StyleMuted is secondary detail a reader can skip: a duration, a target, a
	// timestamp.
	StyleMuted
)

// Styler marks one piece of text. A nil Styler renders plain text, so a caller
// that wants no marking passes nothing.
type Styler interface {
	Mark(style Style, text string) string
}

// Mark applies the styler, or returns the text unchanged when there is none.
func Mark(styler Styler, style Style, text string) string {
	if styler == nil {
		return text
	}
	return styler.Mark(style, text)
}

// Colour is one of the four meanings a palette renders. It is separate from
// Style so a caller can pick a colour for a reason the shared vocabulary does
// not cover, without inventing a new Style for it.
type Colour int

const (
	// Dim is structure and skippable detail.
	Dim Colour = iota
	// Green is work that succeeded.
	Green
	// Red is work that failed.
	Red
	// Yellow is a state to notice: neither a success nor a failure.
	Yellow
)

// Palette renders text with colour when its destination is a terminal.
//
// The decision is made per destination, not per process. fatih/color reads
// os.Stdout once at start-up, which is wrong for a command whose writer is a
// buffer in a test or a file in a redirected run: a redirected run must stay
// plain text, which is what a log wants. The NO_COLOR convention is honoured
// too, through the library's own switch.
type Palette struct {
	w  io.Writer
	on bool
}

// NewPalette builds the palette for one destination. Colour is enabled only when
// the destination is a terminal and the NO_COLOR convention does not forbid it.
func NewPalette(w io.Writer) Palette {
	return Palette{w: w, on: IsTerminal(w) && !color.NoColor}
}

// Writer is the destination the palette writes to.
func (p Palette) Writer() io.Writer { return p.w }

// Enabled reports whether this palette adds colour.
func (p Palette) Enabled() bool { return p.on }

// Mark implements Styler, so a caller can hand a Palette to a renderer that
// speaks in Styles.
func (p Palette) Mark(style Style, text string) string {
	switch style {
	case StyleLabel, StyleMuted:
		return p.Paint(Dim, text)
	case StyleOK:
		return p.Paint(Green, text)
	case StyleFail:
		return p.Paint(Red, text)
	case StyleWarn:
		return p.Paint(Yellow, text)
	default:
		return text
	}
}

// Paint wraps text in a colour, or returns it unchanged when colour is off or
// the text is empty. Colour is enabled on the instance, so the decision made in
// NewPalette is the only one that counts.
func (p Palette) Paint(colour Colour, text string) string {
	if !p.on || text == "" {
		return text
	}
	c := color.New(attribute(colour))
	c.EnableColor()
	return c.Sprint(text)
}

// Dim marks structure and skippable detail.
func (p Palette) Dim(s string) string { return p.Paint(Dim, s) }

// Green marks work that succeeded.
func (p Palette) Green(s string) string { return p.Paint(Green, s) }

// Red marks work that failed.
func (p Palette) Red(s string) string { return p.Paint(Red, s) }

// Yellow marks a state to notice.
func (p Palette) Yellow(s string) string { return p.Paint(Yellow, s) }

// Printf writes a formatted line to the palette's destination. It exists so a
// caller that has a palette does not also carry the writer around.
func (p Palette) Printf(format string, args ...any) error {
	_, err := fmt.Fprintf(p.w, format, args...)
	return err
}

// attribute maps a Colour to the library's value. The mapping lives here so the
// rest of the program never imports the colour library.
func attribute(colour Colour) color.Attribute {
	switch colour {
	case Green:
		return color.FgGreen
	case Red:
		return color.FgRed
	case Yellow:
		return color.FgYellow
	default:
		return color.FgHiBlack
	}
}

// Duration renders a duration for a human: "235 µs", "27.608 ms", "1.5 s".
//
// Three decimals is the precision a reader can act on; the raw nanoseconds
// humanize would otherwise print are noise. Every surface uses this one
// rendering, so the CLI and the API report a duration the same way.
func Duration(d time.Duration) string {
	return humanize.SIWithDigits(d.Seconds(), 3, "s")
}

// Plural adds the plural suffix unless the count is one, so a report never reads
// "1 migration(s)".
func Plural(count int, noun string) string {
	if count == 1 {
		return noun
	}
	return noun + "s"
}

// PadRight pads s to width with trailing spaces, or returns it unchanged when it
// is already at least that wide.
//
// Padding is applied before colour, never after: an escape code is invisible but
// not zero-width to fmt, so padding a painted string would break the column it
// was meant to hold.
func PadRight(s string, width int) string {
	for len([]rune(s)) < width {
		s += " "
	}
	return s
}
