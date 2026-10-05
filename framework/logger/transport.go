package logger

import (
	"io"

	"go.loglayer.dev/transports/pretty/v3"
	"go.loglayer.dev/transports/structured/v3"
	"go.loglayer.dev/v3"
)

// consoleSink builds the sink a human reads: the colourised renderer on a
// terminal, or one JSON object per line when the format asks for it, behind
// the async worker every sink a request writes to shares.
//
// The console is the only sink with a rendering choice. A file or a collector
// keeps its own form, because what a person reads in a terminal and what a
// machine reads from a log file are two different questions, and answering the
// second with the first would put escape codes in the file. Colour is still
// decided per destination: the renderer sees the real writer, so a redirected
// run and a test buffer stay plain text even though the entries reach it
// through the batch buffer.
func consoleSink(opts Options, w io.Writer) *asyncTransport {
	dst := newBatchWriter(w)
	var inner loglayer.Transport
	if opts.Format == FormatStructured {
		inner = structured.New(structured.Config{
			Writer: dst,
			ID:     "console",
		})
	} else {
		inner = pretty.New(pretty.Config{
			Writer:  dst,
			ID:      "console",
			NoColor: !isTerminal(w),
		})
	}
	return newAsyncTransport(inner, dst)
}

// minLevelTransport drops the entries below a threshold, so a sink can carry
// only the levels it exists for. LogLayer dispatches every entry to every
// transport and leaves the filtering to them.
type minLevelTransport struct {
	inner loglayer.Transport
	min   loglayer.LogLevel
}

func (t *minLevelTransport) SendToLogger(params loglayer.TransportParams) {
	if params.LogLevel < t.min {
		return
	}
	t.inner.SendToLogger(params)
}

func (t *minLevelTransport) ID() string             { return t.inner.ID() }
func (t *minLevelTransport) IsEnabled() bool        { return t.inner.IsEnabled() }
func (t *minLevelTransport) GetLoggerInstance() any { return t.inner.GetLoggerInstance() }

// echoSink is the terminal a deployment that does not name the console still
// gets: a warning or an error reaches the operator who is watching the
// service, even though the entries themselves go to the file or the collector
// the configuration named. It is deliberately not configurable and carries no
// queue — the entries it takes are rare, and a synchronous write keeps the
// one line that says something is wrong on the terminal even if the process
// dies a moment later.
func echoSink(w io.Writer) loglayer.Transport {
	return &minLevelTransport{
		inner: pretty.New(pretty.Config{
			Writer:  w,
			ID:      "echo",
			NoColor: !isTerminal(w),
		}),
		min: loglayer.LogLevelWarn,
	}
}

// levelFor maps a configured level onto the logger's own threshold. The
// configuration has already rejected anything else, so an unknown value is not
// reachable; it is treated as info rather than as "every level", because a
// logger that suddenly emits debug lines is worse than a quiet one.
func levelFor(level Level) loglayer.LogLevel {
	switch level {
	case LevelDebug:
		return loglayer.LogLevelDebug
	case LevelWarn:
		return loglayer.LogLevelWarn
	case LevelError:
		return loglayer.LogLevelError
	case LevelInfo:
		return loglayer.LogLevelInfo
	default:
		return loglayer.LogLevelInfo
	}
}
