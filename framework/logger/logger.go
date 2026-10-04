// Package logger wires the process's logging stack.
//
// One LogLayer core sits behind a *slog.Logger, so application code — and every
// dependency that logs through log/slog — emits into the same pipeline. The
// sinks are the ones the configuration names in log.transport: the console, a
// rotating file, and an OpenTelemetry exporter, in any combination. Feature code
// never touches LogLayer directly; it calls slog, and this package decides where
// the entry goes.
//
// # One frontend
//
// slog is the frontend, and the LogLayer core is an implementation detail of it.
// slog is a standard interface, so a dependency that accepts a *slog.Logger can
// be handed this one and lands in the same pipeline; a handler written against
// slog keeps working. LogLayer's own API is the same information said twice, and
// exposing it would leave a codebase with two logging idioms where the second
// one skips the handler everything else is wired through.
package logger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"

	"go.loglayer.dev/integrations/sloghandler/v3"
	"go.loglayer.dev/v3"

	"time"

	"github.com/riipandi/saka/framework/observer"
)

// Logger is the application logger: the LogLayer core behind it, the sinks it
// writes to, and the slog frontend the rest of the program calls.
//
// Every sink is one the configuration named, so a run writes where the file says
// and nowhere else. The console is the default, which is what a fresh checkout
// and a container that logs to stdout both want.
type Logger struct {
	slog *slog.Logger

	// console, file and otlp are held so Shutdown can release them: the async
	// workers hold queues that only a drain can flush, the file sink owns a
	// descriptor, and the collector owns a queue nothing else can flush.
	console *asyncTransport
	file    *fileSink
	otlp    *otlpSink
}

// Options is everything the logger is built from. Every field is explicit, so
// a caller cannot construct a logger whose sinks it cannot name; the values
// come from whoever resolves the configuration, not from this package reading
// anything itself.
type Options struct {
	// Writer is the console destination. It defaults to stdout, which is where a
	// service logs; a test passes a buffer to read what was emitted.
	Writer io.Writer
	// EchoWriter is where the warning-and-above echo goes when the
	// configuration does not name the console. It defaults to stderr; a test
	// passes a buffer to read what the terminal would show.
	EchoWriter io.Writer

	// Level is the threshold every sink writes at.
	Level Level
	// Format is the console rendering choice; a file or a collector keeps
	// JSON regardless.
	Format Format
	// Transports lists the sinks in write order. The list is what switches a
	// sink on, so there is no second flag that could disagree with it.
	Transports []Transport

	// File configures the file sink.
	File FileOptions
	// OTLP configures the collector sink.
	OTLP OTLPOptions

	// ServiceName and Version attribute the log records to the service that
	// produced them; Environment names the deployment when one is set.
	ServiceName string
	Version     string
	Environment string

	// FilePath is where the file sink writes. It is resolved by the caller:
	// where a process keeps its data is the caller's layout decision, and a
	// second derivation inside the framework is a second path that could
	// disagree with it.
	FilePath string
}

// FileOptions configures the rotating file sink.
type FileOptions struct {
	// MaxSize is one file's size in megabytes before it rotates.
	MaxSize int
	// MaxBackups is how many rotated files are kept.
	MaxBackups int
	// MaxAge is how many days a rotated file is kept.
	MaxAge int
	// Compress rotates with gzip.
	Compress bool
}

// OTLPOptions configures the collector sink.
type OTLPOptions struct {
	// Endpoint is the OTLP/HTTP address the exporter dials.
	Endpoint string
	// Headers are the headers every export carries.
	Headers map[string]string
	// Compression is the exporter's body compression.
	Compression observer.Compression
	// QueueMaxSize is how many records buffer before the oldest drops.
	QueueMaxSize int
	// Timeout bounds one export attempt.
	Timeout time.Duration
	// Path is the collector route. Empty means the protocol's own.
	Path string
	// Secure marks an https endpoint, so the exporter loads TLS material.
	Secure bool
}

// Option adjusts Options.
type Option func(*Options)

// WithWriter sends the console rendering somewhere other than stdout.
func WithWriter(w io.Writer) Option {
	return func(o *Options) { o.Writer = w }
}

// WithEchoWriter sends the console-less warning echo somewhere other than
// stderr.
func WithEchoWriter(w io.Writer) Option {
	return func(o *Options) { o.EchoWriter = w }
}

// New builds the logger the configuration describes.
//
// It fails rather than degrading: a sink that cannot be built is an error, not a
// silent downgrade to the console, because a deployment that asked for a file or
// a collector and did not get one has lost the logs it is being trusted with.
// Nothing is left open when it fails part way through.
func New(opts Options, fns ...Option) (*Logger, error) {
	if opts.Writer == nil {
		opts.Writer = os.Stdout
	}
	if opts.EchoWriter == nil {
		opts.EchoWriter = os.Stderr
	}
	for _, apply := range fns {
		apply(&opts)
	}

	l := &Logger{}
	transports := make([]loglayer.Transport, 0, len(opts.Transports))

	// The list is walked in order, so the configuration says both which sinks
	// exist and in what order they are written. Duplicates are refused by
	// Validate, so each one appears at most once here.
	for _, name := range opts.Transports {
		switch name {
		case TransportConsole:
			l.console = consoleSink(opts, opts.Writer)
			transports = append(transports, l.console)

		case TransportFile:
			file, err := newFileSink(opts)
			if err != nil {
				return nil, errors.Join(err, l.release())
			}
			l.file = file
			transports = append(transports, file.async)

		case TransportOTLP:
			otlp, err := newOTLPSink(opts)
			if err != nil {
				return nil, errors.Join(err, l.release())
			}
			l.otlp = otlp
			transports = append(transports, otlp.transport)

		default:
			// Validate refuses an unknown name, so this is only reachable from a
			// configuration nobody checked. It fails rather than skipping the
			// entry: a sink that was asked for and quietly not built is the
			// failure the transport list exists to prevent.
			return nil, fmt.Errorf("logger: log.transport: unknown transport %q", name)
		}
	}
	if len(transports) == 0 {
		return nil, errors.New("logger: log.transport: no transport configured")
	}

	// A run whose transports do not name the console is silent on the
	// terminal by configuration, but an operator watching the service still
	// has to hear when something is wrong. The echo sink carries warnings and
	// errors to stderr — nothing else, so a file-only deployment does not get
	// a second copy of every request line.
	if !slices.Contains(opts.Transports, TransportConsole) {
		transports = append(transports, echoSink(opts.EchoWriter))
	}

	core, err := loglayer.Build(loglayer.Config{
		Transports: transports,
		Level:      levelFor(opts.Level),
		// The recommended serializer: a wrapped or joined error becomes a
		// `causes` array, so the chain an errors.Join built reaches the entry
		// instead of only its top message.
		ErrorSerializer: loglayer.UnwrappingErrorSerializer,
		// Persistent fields and per-call metadata merge at the root rather than
		// nesting under one key, so an entry reads the way slog's own JSON
		// handler writes it. slog is the frontend every caller uses, so its
		// shape is the one to match.
		FlattenMetadata: true,
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("logger: %w", err), l.release())
	}
	l.slog = slog.New(sourceGate{sloghandler.New(core)})
	return l, nil
}

// sourceGate is the wrapper around the slog handler that keeps the call site
// a debug-only fact. The handler behind it forwards Record.PC on every entry
// — the capture cost is already paid by slog — but the function, file, and
// line of the emitting call are sensitive: a file path tells a reader where
// the code lives. Entries above debug reach the sinks without it; a debug
// entry, which is the one a developer turns on to trace a problem, keeps it.
//
// The gate sits at the frontend rather than on any sink, so the rule holds
// for every destination at once — the console, the file, the collector, the
// echo — instead of a policy each transport would have to remember.
type sourceGate struct {
	inner slog.Handler
}

func (g sourceGate) Enabled(ctx context.Context, level slog.Level) bool {
	return g.inner.Enabled(ctx, level)
}

func (g sourceGate) Handle(ctx context.Context, r slog.Record) error {
	if r.Level > slog.LevelDebug {
		// The record is a copy the handler owns; zeroing the PC here is the
		// documented way to keep the call site out of what follows.
		r.PC = 0
	}
	return g.inner.Handle(ctx, r)
}

func (g sourceGate) WithAttrs(attrs []slog.Attr) slog.Handler {
	return sourceGate{inner: g.inner.WithAttrs(attrs)}
}

func (g sourceGate) WithGroup(name string) slog.Handler {
	return sourceGate{inner: g.inner.WithGroup(name)}
}

// Slog returns the logger the application calls.
//
// This is the frontend, and the one to reach for. Bind it once where the
// dependency is wired and call it directly:
//
//	sl := log.Slog()
//	sl.InfoContext(ctx, "served", "status", 200)
//	sl.With("request_id", id).Info("handled")
//
// Prefer the *Context methods wherever a context is in hand: the OpenTelemetry
// sink correlates the record with the span the context carries, and a plain
// Info writes a record with no trace ID.
//
// Slog returns the same *slog.Logger every time, so holding it in a struct field
// is what a component does with it. Writing log.Slog().Info(...) at a call site
// works and is how a single line is emitted from a function that has nothing
// else to say, but a caller that logs more than once should bind it rather than
// reach through the Logger on every call.
//
// With(...) and WithGroup(...) are slog's own, and they work here: the handler
// behind this logger implements them, so a field added through With reaches
// every later record and a group nests what follows. That is the idiom to use
// for request-scoped fields — see TestSlogChainCarriesFieldsThroughThePipeline.
func (l *Logger) Slog() *slog.Logger { return l.slog }

// SetDefault installs the logger as the process default, so a package that logs
// through slog without being handed one lands in the same pipeline.
func (l *Logger) SetDefault() { slog.SetDefault(l.slog) }

// Flush drains every async sink without stopping it: entries queued before
// the call are written and the batch buffers are pushed out when Flush
// returns. It is what a test reads its destination after, and what a caller
// that hands the log to a pipe it is about to close needs.
func (l *Logger) Flush() {
	if l.console != nil {
		l.console.waitDrained()
	}
	if l.file != nil {
		l.file.async.waitDrained()
	}
}

// Shutdown flushes and releases every sink. It is safe to call more than once,
// and a second call does nothing.
//
// The collector is drained first, with the caller's context: its batch
// processor holds queued records that nothing else can flush, so a shutdown
// that skips it loses whatever was queued. The async sinks come next — each
// drain writes every queued entry and pushes the batch buffer out — and the
// inner transports are released only after their queue is empty, so a
// graceful stop loses nothing.
func (l *Logger) Shutdown(ctx context.Context) error {
	var errs []error
	if l.otlp != nil {
		if err := l.otlp.shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
		l.otlp = nil
	}
	errs = append(errs, l.closeSink(ctx, l.console, "console")...)
	l.console = nil
	errs = append(errs, l.closeSink(ctx, l.fileAsync(), "file")...)
	l.file = nil
	return errors.Join(errs...)
}

// fileAsync exposes the file sink's worker for Shutdown, nil when absent.
func (l *Logger) fileAsync() *asyncTransport {
	if l.file == nil {
		return nil
	}
	return l.file.async
}

// closeSink drains and releases one async sink under the caller's context.
// The wait is bounded: a destination that stopped accepting must not hang
// the shutdown past the context, and the drain that misses the deadline is
// reported rather than silently abandoned.
func (l *Logger) closeSink(ctx context.Context, sink *asyncTransport, name string) []error {
	if sink == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- sink.Close() }()
	var errs []error
	select {
	case err := <-done:
		if err != nil {
			errs = append(errs, err)
		}
	case <-ctx.Done():
		errs = append(errs, fmt.Errorf("logger: %s sink drain: %w", name, ctx.Err()))
	}
	return errs
}

// release closes every sink that was built before a later one failed. The sinks
// are already unusable, so a failure here is secondary to the one that stopped
// construction and is joined rather than raised.
func (l *Logger) release() error {
	return l.Shutdown(context.Background())
}
