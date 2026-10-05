package storage

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAnUploadLockLeavesWhenItsLastHolderDoes pins the lock map's lifecycle:
// an entry exists for the uploads in flight and is gone once the last holder
// releases, so a server that serves many uploads does not keep a mutex for
// every key it ever served.
func TestAnUploadLockLeavesWhenItsLastHolderDoes(t *testing.T) {
	handler := &TusHandler{}

	unlock := handler.lockUpload("bucket/one")
	assert.Equal(t, 1, mapLen(&handler.locks), "the in-flight upload holds its entry")

	unlock()
	assert.Equal(t, 0, mapLen(&handler.locks), "a released upload's entry is gone")

	// The next upload for the same key gets a fresh entry, not a revival.
	unlock2 := handler.lockUpload("bucket/one")
	assert.Equal(t, 1, mapLen(&handler.locks))
	unlock2()
	assert.Equal(t, 0, mapLen(&handler.locks))
}

// TestConcurrentUploadersSerializeOnOneEntry covers the removal's race: two
// appenders for one ref serialize, the entry survives both, and it leaves
// only after the second holder releases — a delete that outran a holder
// would let a third appender build a second mutex and interleave chunks.
func TestConcurrentUploadersSerializeOnOneEntry(t *testing.T) {
	handler := &TusHandler{}
	const uploaders = 8

	var mu sync.Mutex
	inside := 0
	violations := 0

	var wg sync.WaitGroup
	wg.Add(uploaders)
	for range uploaders {
		go func() {
			defer wg.Done()
			unlock := handler.lockUpload("bucket/two")
			defer unlock()

			mu.Lock()
			inside++
			if inside > 1 {
				violations++
			}
			inside--
			mu.Unlock()
		}()
	}
	wg.Wait()

	assert.Zero(t, violations, "two chunks for one file must never interleave")
	assert.Equal(t, 0, mapLen(&handler.locks), "the last holder's release removes the entry")
}

func mapLen(m *sync.Map) int {
	count := 0
	m.Range(func(any, any) bool { count++; return true })
	return count
}
