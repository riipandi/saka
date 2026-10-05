package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/riipandi/saka/framework/logger"
	"github.com/riipandi/saka/framework/observer"
	"github.com/riipandi/saka/internal/config"
)

// # The process logger

// loggerState holds the logger for this run, built on first use.
//
// It is built lazily because the configuration may not resolve yet: the root
// Before runs before every command, including the two that bootstrap a fresh
// checkout — config:generate writes the file, and key:generate runs before the
// variables the file references exist. A command that needs neither the
// configuration nor logging must not fail because a logger could not be built.
type loggerState struct {
	once sync.Once

	log *logger.Logger
	err error
	cfg config.Config
}

// loggerFrom returns the process logger, building it on first call.
//
// A failure is returned rather than swallowed: a command that asked for a logger
// and did not get one should say so, because the alternative is a run that looks
// healthy and writes nowhere.
func loggerFrom(ctx context.Context) (*logger.Logger, error) {
	state, ok := ctx.Value(loggerKey{}).(*loggerState)
	if !ok {
		return nil, errors.New("logger: not installed for this command")
	}

	state.once.Do(func() {
		state.log, state.err = logger.New(state.cfg.LoggerOptions())
		if state.err == nil {
			state.log.SetDefault()
		}
	})
	return state.log, state.err
}

// loggerKey is the context key the logger state is stored under. It is a private
// struct type so no other package can collide with it.
type loggerKey struct{}

// # The process observer
//
// Traces and metrics follow the logger: the same lazy state, the same context
// key, the same close at the end of a run. They are kept apart because a command
// that logs has no reason to build a tracer, and a command that reports a
// measurement has no reason to open a file sink.

// observerState holds the observer for this run, built on first use. The reason
// it is lazy is the logger's: the configuration may not resolve yet, and a
// command that needs neither a signal nor the configuration must not fail
// because one could not be built.
type observerState struct {
	once sync.Once

	observer *observer.Observer
	err      error
	cfg      config.Config
}

// observerFrom returns the process observer, building it on first call.
func observerFrom(ctx context.Context) (*observer.Observer, error) {
	state, ok := ctx.Value(observerKey{}).(*observerState)
	if !ok {
		return nil, errors.New("observer: not installed for this command")
	}

	state.once.Do(func() {
		state.observer, state.err = observer.New(context.WithoutCancel(ctx), state.cfg.ObserverOptions())
		if state.err == nil {
			state.observer.SetGlobals()
		}
	})
	return state.observer, state.err
}

// observerKey is the context key the observer state is stored under.
type observerKey struct{}

// installObserver puts the observer state on the context, beside the logger's.
func installObserver(ctx context.Context, cmd *cli.Command) context.Context {
	cfg, err := configFrom(ctx)
	if err != nil {
		return context.WithValue(ctx, observerKey{}, &observerState{err: err})
	}
	return context.WithValue(ctx, observerKey{}, &observerState{cfg: cfg})
}

// closeObserver drains every queued span and measurement at the end of a run.
//
// It gets its own deadline for the logger's reason: a cancelled run reaches here
// with a context that is already done, and the queues are the one thing that
// still has to be drained. It is a no-op when no command built an observer,
// which is the common case.
//
// A failure to drain is reported, never returned. Telemetry is a side channel:
// a collector that is down at shutdown means some spans and measurements were
// lost, which is worth saying and is not a reason for the command to fail. The
// alternative — a non-zero exit because a collector was unreachable — would
// report an application that worked as one that did not.
func closeObserver(ctx context.Context) error {
	state, ok := ctx.Value(observerKey{}).(*observerState)
	if !ok || state.observer == nil {
		return nil
	}

	flush, cancel := context.WithTimeout(context.WithoutCancel(ctx), observerShutdownTimeout)
	defer cancel()
	if err := state.observer.Shutdown(flush); err != nil {
		reportTelemetryLoss(ctx, "observer", err)
	}
	return nil
}

// observerShutdownTimeout bounds the final drain. The metric reader's own
// timeout defaults to the same ten seconds, and PeriodicReader.Shutdown gives
// the caller's context priority over its own, so this deadline is the one that
// actually stops a drain that will not finish.
const observerShutdownTimeout = 10 * time.Second

// reportTelemetryLoss records a shutdown that could not drain a queue. It writes
// through the logger when one exists, because that is the channel a user reads,
// and falls back to stderr when the logger itself is what failed to drain.
func reportTelemetryLoss(ctx context.Context, component string, err error) {
	if state, ok := ctx.Value(loggerKey{}).(*loggerState); ok && state.log != nil {
		state.log.Slog().WarnContext(ctx, "telemetry dropped at shutdown", "component", component, "err", err.Error())
		return
	}
	fmt.Fprintf(os.Stderr, "%s: telemetry dropped at shutdown: %v\n", component, err)
}

// installLogger builds the logger the configuration describes and puts it on the
// context, so every command that logs lands in the same pipeline.
//
// The configuration is resolved here rather than through configFrom, because a
// failure at this point is not the command's problem: the state keeps the error
// and the command that actually needs logging reports it.
func installLogger(ctx context.Context, cmd *cli.Command) context.Context {
	cfg, err := configFrom(ctx)
	if err != nil {
		// The configuration did not resolve, so there is nothing to build a
		// logger from. The state is still installed, holding the failure, so a
		// command that asks for one is told why rather than getting a nil.
		return context.WithValue(ctx, loggerKey{}, &loggerState{err: err})
	}
	return context.WithValue(ctx, loggerKey{}, &loggerState{cfg: cfg})
}

// closeLogger flushes the logger at the end of a run. It is a no-op when no
// command ever built one, which is the common case for the bootstrap commands.
//
// The flush gets its own deadline rather than the run's context. A run that was
// cancelled — a signal, a deadline — reaches here with a context that is already
// done, and the collector's queue is the one thing that still has to be drained:
// handing a cancelled context to it would drop every queued record at exactly
// the moment a service is being shut down. The deadline is what bounds the
// flush instead.
func closeLogger(ctx context.Context) error {
	state, ok := ctx.Value(loggerKey{}).(*loggerState)
	if !ok || state.log == nil {
		return nil
	}

	flush, cancel := context.WithTimeout(context.WithoutCancel(ctx), loggerShutdownTimeout)
	defer cancel()
	return state.log.Shutdown(flush)
}

// loggerShutdownTimeout bounds the final flush. It is short because a shutdown
// path is one a caller is waiting on, and long enough for one batch to leave.
const loggerShutdownTimeout = 5 * time.Second
