package logger

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"go.loglayer.dev/v3"
)

// recordingTransport stands in for the real sinks: it captures the messages
// it is handed, optionally slowly, and renders into out when one is set —
// the way the real transports write their rendering into the batch writer.
// gate, when set, blocks every send until it is closed, so a test can queue
// a burst behind a worker that is pinned mid-entry.
type recordingTransport struct {
	id    string
	delay time.Duration
	out   io.Writer
	gate  chan struct{}

	mu   sync.Mutex
	msgs []string
}

func (r *recordingTransport) ID() string             { return r.id }
func (r *recordingTransport) IsEnabled() bool        { return true }
func (r *recordingTransport) GetLoggerInstance() any { return nil }

func (r *recordingTransport) SendToLogger(params loglayer.TransportParams) {
	if r.gate != nil {
		<-r.gate
	}
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	msg := fmt.Sprint(params.Messages...)
	if r.out != nil {
		_, _ = fmt.Fprintln(r.out, msg)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
}

func (r *recordingTransport) written() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.msgs...)
}

// TestCloseDrainsEveryQueuedEntry pins the shutdown guarantee: a Close must
// write what producers already queued, so a graceful stop loses nothing.
func TestCloseDrainsEveryQueuedEntry(t *testing.T) {
	inner := &recordingTransport{id: "test", delay: time.Microsecond}
	a := newAsyncTransport(inner, nil)

	const n = 2000
	for i := range n {
		a.SendToLogger(loglayer.TransportParams{Messages: []any{fmt.Sprint(i)}})
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	got := inner.written()
	if len(got) != n {
		t.Fatalf("close drained %d of %d queued entries", len(got), n)
	}
}

// TestSendAfterCloseStillWrites pins the post-close fallback: an entry that
// races with, or arrives after, the shutdown is written synchronously rather
// than dropped.
func TestSendAfterCloseStillWrites(t *testing.T) {
	inner := &recordingTransport{id: "test"}
	a := newAsyncTransport(inner, nil)
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	a.SendToLogger(loglayer.TransportParams{Messages: []any{"late"}})

	if got := inner.written(); len(got) != 1 || got[0] != "late" {
		t.Fatalf("post-close entry lost: %v", got)
	}
}

// TestConcurrentEmittersLoseNothing pins the no-drop guarantee under the load
// shape that motivates the wrapper: many emitters, more entries than the
// queue holds, so the queue fills and the emitters block. Every entry must
// still arrive.
func TestConcurrentEmittersLoseNothing(t *testing.T) {
	inner := &recordingTransport{id: "test", delay: 10 * time.Microsecond}
	a := newAsyncTransport(inner, nil)

	const emitters, perEmitter = 16, 3000
	var wg sync.WaitGroup
	for e := range emitters {
		wg.Go(func() {
			for i := range perEmitter {
				a.SendToLogger(loglayer.TransportParams{
					Messages: []any{fmt.Sprintf("%d-%d", e, i)},
				})
			}
		})
	}
	wg.Wait()
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := len(inner.written()); got != emitters*perEmitter {
		t.Fatalf("burst dropped entries: wrote %d of %d", got, emitters*perEmitter)
	}
}

// TestOrderFollowsTheEmitter pins the ordering a single worker gives: what
// one goroutine queued in sequence reaches the sink in that sequence.
func TestOrderFollowsTheEmitter(t *testing.T) {
	inner := &recordingTransport{id: "test"}
	a := newAsyncTransport(inner, nil)

	const n = 500
	for i := range n {
		a.SendToLogger(loglayer.TransportParams{Messages: []any{i}})
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	for i, msg := range inner.written() {
		if msg != fmt.Sprint(i) {
			t.Fatalf("order broken at %d: got %q", i, msg)
		}
	}
}

// TestWaitDrainedFlushesTheBatch pins Flush without shutdown: entries queued
// before the call are on the destination when it returns.
func TestWaitDrainedFlushesTheBatch(t *testing.T) {
	sink := &strings.Builder{}
	bw := newBatchWriter(sink)
	inner := &recordingTransport{id: "test", out: bw}
	a := newAsyncTransport(inner, bw)

	const n = 100
	for i := range n {
		a.SendToLogger(loglayer.TransportParams{Messages: []any{fmt.Sprint(i)}})
	}
	a.waitDrained()

	got := sink.String()
	for i := range n {
		if !strings.Contains(got, fmt.Sprint(i)) {
			t.Fatalf("entry %d missing after Flush", i)
		}
	}
}

// countingWriter counts the write calls the destination sees, so the batching
// is observable.
type countingWriter struct {
	mu    sync.Mutex
	calls int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	return len(p), nil
}

func (w *countingWriter) writeCalls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

// TestBatchingAmortizesTheSyscall pins what the load test bought: a burst
// rendered through the batch writer reaches the destination in a handful of
// writes, not one per entry. The worker is pinned mid-entry while the burst
// queues, so the coalescing is deterministic rather than a race the producer
// sometimes loses.
func TestBatchingAmortizesTheSyscall(t *testing.T) {
	dst := &countingWriter{}
	bw := newBatchWriter(dst)
	gate := make(chan struct{})
	inner := &recordingTransport{id: "test", out: bw, gate: gate}
	a := newAsyncTransport(inner, bw)

	a.SendToLogger(loglayer.TransportParams{Messages: []any{"pinned"}})
	const n = 1000
	for i := range n {
		a.SendToLogger(loglayer.TransportParams{Messages: []any{fmt.Sprint(i)}})
	}
	close(gate)
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if calls := dst.writeCalls(); calls > 10 {
		t.Fatalf("%d writes for %d entries: batching did not hold", calls, n)
	}
}

// TestSendDuringCloseDoesNotShareTheWriter pins the shutdown window: producers
// keep sending while Close runs, and the inner sink is never written from
// two goroutines at once.
func TestSendDuringCloseDoesNotShareTheWriter(t *testing.T) {
	inner := &recordingTransport{id: "test", delay: time.Microsecond}
	a := newAsyncTransport(inner, nil)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				a.SendToLogger(loglayer.TransportParams{Messages: []any{"line"}})
			}
		})
	}
	wg.Go(func() {
		time.Sleep(time.Millisecond)
		if err := a.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	wg.Wait()
}

// TestCloseIsIdempotent pins the contract the shutdown paths rely on: the
// second Close re-reports the first result instead of draining again.
func TestCloseIsIdempotent(t *testing.T) {
	inner := &recordingTransport{id: "test"}
	a := newAsyncTransport(inner, nil)
	a.SendToLogger(loglayer.TransportParams{Messages: []any{"once"}})

	if err := a.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if got := inner.written(); len(got) != 1 {
		t.Fatalf("expected exactly one entry, got %d", len(got))
	}
}

// The wrapper must be invisible to the core that dispatches by ID.
func TestWrapperForwardsTheTransportIdentity(t *testing.T) {
	inner := &recordingTransport{id: "console"}
	a := newAsyncTransport(inner, nil)
	defer func() { _ = a.Close() }()

	if a.ID() != "console" {
		t.Fatalf("ID: got %q", a.ID())
	}
	if !a.IsEnabled() {
		t.Fatal("IsEnabled: got false")
	}
	if a.GetLoggerInstance() != nil {
		t.Fatal("GetLoggerInstance: expected nil passthrough")
	}
}

// The batch writer is an io.Writer the transports render into.
var _ io.Writer = (*batchWriter)(nil)
