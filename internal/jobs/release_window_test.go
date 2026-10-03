package jobs_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/riipandi/saka/internal/config"
	"github.com/riipandi/saka/internal/jobs"
)

// TestEveryJobFitsTheDefaultReleaseWindow is the invariant the queue depends
// on, asserted against the real inputs. queue.Client.Register refuses a queue
// whose Timeout reaches the client's ReleaseAfter, because a task still
// running when its claim is released is handed to a second worker and runs
// twice.
//
// This is the check that would have caught the state it was written for: a
// 30-minute chunk upload under a 10-minute release window. A job that grows
// past the window now fails here rather than in production.
func TestEveryJobFitsTheDefaultReleaseWindow(t *testing.T) {
	release := config.DefaultQueueReleaseAfter
	assert.Positive(t, release)

	for _, tc := range []struct {
		name    string
		timeout time.Duration
	}{
		{"cleanup", jobs.CleanupTask{}.Config().Timeout},
		{"storage upload", jobs.ChunkUploadTask{}.Config().Timeout},
		{"storage gc", jobs.StorageGCTask{}.Config().Timeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.timeout <= 0 {
				return
			}
			assert.Less(t, tc.timeout, release,
				"job %q runs for %s, which is not below the default release window %s: "+
					"raise config.DefaultQueueReleaseAfter",
				tc.name, tc.timeout, release)
		})
	}
}

// TestTheScheduledJobsFitTheDefaultReleaseWindow guards the second way a queue
// reaches the engine: the jobs the cron scheduler enqueues. The timeout is the
// enqueued task's own, because that is the queue the dispatcher runs it on.
func TestTheScheduledJobsFitTheDefaultReleaseWindow(t *testing.T) {
	release := config.DefaultQueueReleaseAfter

	for _, job := range jobs.Scheduled() {
		if job.Task == nil {
			continue
		}
		timeout := job.Task.Config().Timeout
		if timeout <= 0 {
			continue
		}
		assert.Less(t, timeout, release,
			"scheduled job %q runs for %s, past the release window %s",
			job.Name, timeout, release)
	}
}
