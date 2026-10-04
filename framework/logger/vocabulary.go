package logger

// Level is the threshold a logger writes at. The values are the configuration
// vocabulary the schema validates against.
type Level string

const (
	// LevelDebug emits everything, with the call site attached.
	LevelDebug Level = "debug"
	// LevelInfo is the operational default.
	LevelInfo Level = "info"
	// LevelWarn emits warnings and above.
	LevelWarn Level = "warn"
	// LevelError emits errors only.
	LevelError Level = "error"
)

// Levels lists every level name, for a schema that validates the value.
func Levels() []string {
	return []string{string(LevelDebug), string(LevelInfo), string(LevelWarn), string(LevelError)}
}

// Format is the console rendering choice. A file or a collector keeps JSON
// regardless: what a person reads in a terminal and what a machine reads from
// a log file are different questions.
type Format string

const (
	// FormatPretty renders the console for a person.
	FormatPretty Format = "pretty"
	// FormatStructured renders the console as JSON.
	FormatStructured Format = "structured"
)

// Formats lists every format name, for a schema that validates the value.
func Formats() []string {
	return []string{string(FormatPretty), string(FormatStructured)}
}

// Transport is the name of a sink. Each one is a sink the transports list
// switches on; an unknown name fails construction rather than being skipped.
type Transport string

const (
	// TransportConsole writes to the terminal.
	TransportConsole Transport = "console"
	// TransportFile writes one JSON object per line to a rotating file.
	TransportFile Transport = "file"
	// TransportOTLP ships entries to an OpenTelemetry collector.
	TransportOTLP Transport = "otlp"
)

// TransportNames lists every transport name, for a schema that validates the
// value.
func TransportNames() []string {
	return []string{string(TransportConsole), string(TransportFile), string(TransportOTLP)}
}
