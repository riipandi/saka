package storage

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// settleSpy collects the keys the watcher settles, the observable of every
// watcher test.
type settleSpy struct {
	mu   sync.Mutex
	keys []string
}

func (s *settleSpy) settled(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, key)
}

func (s *settleSpy) has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.keys, key)
}

// startWatcher runs a watcher over a staging directory with a short debounce
// and returns the spy its settles land in.
func startWatcher(t *testing.T, debounce time.Duration) (context.CancelFunc, *settleSpy, string) {
	t.Helper()

	staging := t.TempDir()
	spy := &settleSpy{}
	ctx, cancel := context.WithCancel(t.Context())
	go func() { _ = NewWatcher(staging, debounce, slog.New(slog.DiscardHandler), spy.settled).Start(ctx) }()
	t.Cleanup(cancel)
	return cancel, spy, staging
}

func waitFor(t *testing.T, spy *settleSpy, key string) {
	t.Helper()
	require.Eventually(t, func() bool { return spy.has(key) }, 5*time.Second, 10*time.Millisecond)
}

func TestWatcherSettlesAFileAfterTheDebounceWindow(t *testing.T) {
	_, spy, staging := startWatcher(t, 50*time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(staging, "report.txt"), []byte("v1"), 0o600))
	waitFor(t, spy, "report.txt")
}

func TestWatcherSettlesANestedFile(t *testing.T) {
	// Keys name nested paths — avatar/usr_1/128.png — so a file written
	// into a subdirectory must settle like one at the root, whether the
	// directory predates the watch or appears under it.
	_, spy, staging := startWatcher(t, 50*time.Millisecond)

	require.NoError(t, os.MkdirAll(filepath.Join(staging, "avatar", "usr_1"), 0o755))
	time.Sleep(100 * time.Millisecond) // the watch picks the new directory up
	require.NoError(t, os.WriteFile(filepath.Join(staging, "avatar", "usr_1", "128.png"), []byte("v1"), 0o600))
	waitFor(t, spy, filepath.Join("avatar", "usr_1", "128.png"))
}

func TestWatcherSettlesARenamedInFileOnce(t *testing.T) {
	_, spy, staging := startWatcher(t, 50*time.Millisecond)

	// Stage writes a temp file and renames it: the temp name must never
	// settle, the final name must, however many writes produced it.
	tmp := filepath.Join(staging, ".report.txt.tmp")
	require.NoError(t, os.WriteFile(tmp, []byte("partial"), 0o600))
	require.NoError(t, os.Rename(tmp, filepath.Join(staging, "report.txt")))
	waitFor(t, spy, "report.txt")

	time.Sleep(150 * time.Millisecond)
	assert.False(t, spy.has(".report.txt.tmp"), "a hidden temp file is not a staging file")
}

func TestWatcherDebounceCollapsesABurstOfWritesIntoOneSettle(t *testing.T) {
	// The debounce is the map of timers the loop resets; the filesystem
	// backend's delivery latency must not decide this test, so the burst
	// is driven through the same resetTimer the event loop calls, and the
	// test drains the settle channel the way the loop would.
	w := NewWatcher(t.TempDir(), 60*time.Millisecond, slog.New(slog.DiscardHandler), func(string) {})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	timers := make(map[string]*time.Timer)
	settled := make(chan string, 8)
	// Five resets inside one window: one timer, one settle.
	for range 5 {
		w.resetTimer(ctx, timers, settled, "busy.txt")
		time.Sleep(20 * time.Millisecond)
	}

	select {
	case key := <-settled:
		assert.Equal(t, "busy.txt", key)
	case <-time.After(5 * time.Second):
		t.Fatal("the burst never settled")
	}

	time.Sleep(200 * time.Millisecond)
	select {
	case key := <-settled:
		t.Fatalf("the burst settled twice: %q", key)
	default:
	}
}

func TestWatcherSettlesConcurrentWriters(t *testing.T) {
	// The timer callback only sends on the settle channel. The map that
	// owns the timers stays on the watch loop. Concurrent writers are what
	// would panic if a later change touched that map from the callback.
	_, spy, staging := startWatcher(t, 20*time.Millisecond)

	names := []string{"langdon.txt", "neveu.txt", "vetra.txt", "granger.txt"}
	errs := make(chan error, len(names))
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Go(func() {
			path := filepath.Join(staging, name)
			for range 8 {
				if err := os.WriteFile(path, []byte("horcrux"), 0o600); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for _, name := range names {
		waitFor(t, spy, name)
	}
}

func TestWatcherSettlesTheFilesAlreadyInStaging(t *testing.T) {
	// A file that outlived its process sits in staging when the next run
	// starts: the scan settles it without waiting for an event.
	staging := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staging, "leftover.txt"), []byte("x"), 0o600))

	spy := &settleSpy{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- NewWatcher(staging, time.Minute, slog.New(slog.DiscardHandler), spy.settled).Start(ctx)
	}()
	t.Cleanup(cancel)

	waitFor(t, spy, "leftover.txt")
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not exit after its context was cancelled")
	}
}
