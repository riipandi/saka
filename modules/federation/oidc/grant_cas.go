package oidc

import (
	"context"
	"net/http"
	"sync"
)

// The grant compare-and-swap. A one-time grant's consumption is two
// calls apart — the read the token endpoint performs, the save that
// marks the code consumed — and the pinned library holds no transaction
// between them. The seam that closes the gap is the save itself: each
// request remembers the grant documents it read (and the documents its
// own saves produced), and a save that names a snapshot must find the
// stored row holding exactly that document. Two requests redeeming the
// same code both read the unconsumed document, and only the first save
// finds it — the second is refused before any token is issued.

// grantSnapshots is one request's record of the grant documents it read
// and wrote, keyed by grant id.
type grantSnapshots struct {
	mu   sync.Mutex
	docs map[string][]byte
}

func (gs *grantSnapshots) remember(grantID string, document []byte) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	gs.docs[grantID] = document
}

func (gs *grantSnapshots) expected(grantID string) ([]byte, bool) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	document, ok := gs.docs[grantID]
	return document, ok
}

type grantSnapshotsKey struct{}

// withGrantSnapshotCache puts an empty snapshot record on the request's
// context, the form the store adapters read.
func withGrantSnapshotCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, grantSnapshotsKey{}, &grantSnapshots{docs: map[string][]byte{}})
}

// refreshRotations is one request's record of the refresh-token pointers
// it loaded — the rows a rotation retires when the replacement lands.
type refreshRotations struct {
	mu   sync.Mutex
	keys []string
}

func (rr *refreshRotations) remember(hash string) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.keys = append(rr.keys, hash)
}

func (rr *refreshRotations) retired() []string {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return append([]string(nil), rr.keys...)
}

type refreshRotationsKey struct{}

func refreshRotationsFrom(ctx context.Context) *refreshRotations {
	rotations, _ := ctx.Value(refreshRotationsKey{}).(*refreshRotations)
	return rotations
}

func grantSnapshotsFrom(ctx context.Context) *grantSnapshots {
	snapshots, _ := ctx.Value(grantSnapshotsKey{}).(*grantSnapshots)
	return snapshots
}

// grantSnapshotMiddleware arms the compare-and-swap for one route. The
// library delegates context values to the request's context, so a cache
// planted here reaches the store adapters the handler calls into.
func grantSnapshotMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withGrantSnapshotCache(r.Context())
		ctx = context.WithValue(ctx, refreshRotationsKey{}, &refreshRotations{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
