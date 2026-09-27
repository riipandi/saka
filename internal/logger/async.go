package logger

import (
	"bufio"
	"io"
	"sync"

	"go.loglayer.dev/v3"
)

// The request path emits one log line per request, and a synchronous write
// per line puts a syscall on the critical path of every response — measured
// at a fifth of the throughput a load test reaches without it. The sinks a
// request writes to are therefore wrapped in asyncTransport: an entry is
// handed to a bounded queue and one worker per sink renders and writes it.
//
// The queue never drops. A full queue blocks the caller, so under sustained
// overload the cost of logging shows up as latency, never as a lost line —
// the guarantee the synchronous sink gave, kept by the wrapper. Shutdown
// drains the queue, so a graceful stop loses nothing either; only a kill -9
// between enqueue and drain can lose what the queue still holds, the same
// window any buffering accepts.
//
// The OTLP sink is deliberately not wrapped: its batch processor already
// queues in the background, and a second queue would only defer the same
// drops behind another buffer.
const asyncQueueDepth = 8192

// flusher is the batch writer a sink renders into. The async worker flushes
// it once the queue has drained, so a burst of entries costs one write
// syscall instead of one per line.
type flusher interface {
	Flush() error
}

// asyncTransport wraps a LogLayer transport with a single worker that owns
// it. One worker per sink keeps the entry order the emitter saw, and makes
// the inner transport single-threaded, which removes the writer contention
// concurrent emitters otherwise create.
type asyncTransport struct {
	inner  loglayer.Transport
	flush  flusher // nil when the inner transport owns its writer
	queue  chan loglayer.TransportParams
	drains chan chan struct{} // flush requests, served after the queued entries
	done   chan struct{}      // closed by Close to stop accepting and drain
	exited chan struct{}      // closed by the worker when the drain finished

	mu      sync.RWMutex
	closed  bool
	once    sync.Once
	closeEr error
}

// newAsyncTransport starts the worker for inner. A caller that hands a
// flusher gets the entries written in batches; one that does not leaves the
// inner transport's own writer in charge.
func newAsyncTransport(inner loglayer.Transport, f flusher) *asyncTransport {
	a := &asyncTransport{
		inner:  inner,
		flush:  f,
		queue:  make(chan loglayer.TransportParams, asyncQueueDepth),
		drains: make(chan chan struct{}),
		done:   make(chan struct{}),
		exited: make(chan struct{}),
	}
	go a.work()
	return a
}

// SendToLogger hands the entry to the worker. It blocks while the queue is
// full — the point at which a slower disk or terminal pushes back on the
// application rather than on the log — and falls back to a synchronous send
// once the transport has been closed, so an entry that arrives during or
// after shutdown is written, never dropped.
//
// The lock covers the closed check and the enqueue together. Close takes the
// same lock before it closes done, so a send cannot observe "still open" and
// then write the inner sink while the worker is draining it. A send that
// finds the transport already closed waits until that drain has finished.
func (a *asyncTransport) SendToLogger(params loglayer.TransportParams) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		<-a.exited
		a.inner.SendToLogger(params)
		return
	}
	a.queue <- params
	a.mu.Unlock()
}

// work renders and writes entries until Close stops accepting, then drains
// whatever is queued. Close sets the closed flag under the same lock a send
// holds across its enqueue, and only then closes done, so this drain does
// not share the inner writer with a producer.
func (a *asyncTransport) work() {
	defer close(a.exited)
	for {
		select {
		case params := <-a.queue:
			a.inner.SendToLogger(params)
			a.flushBatch(len(a.queue) == 0)
		case wait := <-a.drains:
			a.drainQueue()
			a.flushBatch(true)
			close(wait)
		case <-a.done:
			a.drainQueue()
			a.flushBatch(true)
			return
		}
	}
}

// drainQueue writes every entry queued at the moment of the call. A producer
// racing with it is served by a later drain; nobody loses an entry.
func (a *asyncTransport) drainQueue() {
	for {
		select {
		case params := <-a.queue:
			a.inner.SendToLogger(params)
		default:
			return
		}
	}
}

// flushBatch empties the batch writer to the real destination. An interim
// flush runs only when the queue has emptied, so a burst renders into the
// buffer and costs a single write; the final flush of a drain always runs.
func (a *asyncTransport) flushBatch(final bool) {
	if a.flush == nil || (!final && len(a.queue) > 0) {
		return
	}
	_ = a.flush.Flush()
}

// ID, IsEnabled and GetLoggerInstance forward to the wrapped transport: the
// wrapper is invisible to the core that dispatches by ID and reads the
// underlying logger.
func (a *asyncTransport) ID() string             { return a.inner.ID() }
func (a *asyncTransport) IsEnabled() bool        { return a.inner.IsEnabled() }
func (a *asyncTransport) GetLoggerInstance() any { return a.inner.GetLoggerInstance() }

// Close stops accepting entries, waits for the worker to drain the queue and
// flush the batch, and releases the inner transport's resources. LogLayer
// calls it to pre-flush async transports before a Fatal exit, and Shutdown
// calls it at the end of the process. Idempotent.
func (a *asyncTransport) Close() error {
	a.once.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.mu.Unlock()
		close(a.done)
		<-a.exited
		a.closeEr = closeTransport(a.inner)
	})
	return a.closeEr
}

// waitDrained returns once every entry queued before the call has been
// written and the batch writer flushed, without stopping the sink. Flush is
// built on it: a test reads the destination right after it returns.
func (a *asyncTransport) waitDrained() {
	select {
	case <-a.exited:
		return
	default:
	}
	wait := make(chan struct{})
	select {
	case a.drains <- wait:
		<-wait
	case <-a.exited:
	}
}

// closeTransport releases an inner transport that owns a resource. The
// LogLayer Transport interface has no Close, so the sinks that hold one are
// found by the same type assertion the core itself uses to flush.
func closeTransport(t loglayer.Transport) error {
	if c, ok := t.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// batchWriter renders into a memory buffer and writes to the destination in
// batches, so a burst of entries costs one syscall instead of one per line.
// The buffer is written through on every drain — every time the queue runs
// empty — so what a kill -9 could lose is one batch, never one buffer.
type batchWriter struct {
	buf *bufio.Writer
}

func newBatchWriter(dst io.Writer) *batchWriter {
	return &batchWriter{buf: bufio.NewWriterSize(dst, 64*1024)}
}

func (w *batchWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

// Flush pushes the buffered bytes out. An empty buffer is a no-op, so the
// per-drain cost when the application is quiet is one size check.
func (w *batchWriter) Flush() error { return w.buf.Flush() }
