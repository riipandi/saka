# Tango Codebase Deep Analysis & Audit Report

**Date:** 2026-09-28  
**Scope:** Complete Go codebase analysis for race conditions, memory leaks, security vulnerabilities, and performance optimization opportunities  
**Analyzed Directories:** `internal/`, `modules/`, `pkg/`, `cmd/`, `database/`

---

## Executive Summary

The tango codebase demonstrates **strong engineering practices** with proper use of parameterized queries, consistent resource cleanup, context propagation, and security-conscious design. However, **1 HIGH severity issue** and **3 MEDIUM severity issues** were identified that should be addressed to improve reliability and performance.

**Severity Breakdown:**

- **Critical:** 0
- **High:** 1
- **Medium:** 3  
- **Low:** 6

---

## 1. RACE CONDITIONS & CONCURRENCY ISSUES

### 1.1 Data Race in Watcher Timer Map ⚠️ **HIGH**

**File:** `internal/storage/watcher.go`  
**Lines:** 70-77, 110-115, 139-150

**Issue:** The `timers` map is accessed concurrently without proper synchronization. The `resetTimer` function (called from event handler goroutine) creates and modifies timers, while the main loop reads and deletes from the map. The comment claiming "no lock" is incorrect.

**Current Code:**

```go
timers := make(map[string]*time.Timer)
settled := make(chan string, 64)
for {
    select {
    case <-ctx.Done():
        for _, timer := range timers {
            timer.Stop()
        }
        return nil
    case key := <-settled:
        delete(timers, key)  // Unsafe concurrent access
```

**Recommendation:** Protect the `timers` map with `sync.Mutex`:

```go
type Watcher struct {
    // ... existing fields
    timersMu sync.Mutex
    timers   map[string]*time.Timer
}

func (w *Watcher) resetTimer(ctx context.Context, timers map[string]*time.Timer, settled chan<- string, key string) {
    w.timersMu.Lock()
    defer w.timersMu.Unlock()
    // ... rest of the function
}
```

**Impact:** Under concurrent file system events, this could cause panics or timer leaks.

---

### 1.2 Busy-Wait Loop in Queue Dispatcher ⚠️ **MEDIUM**

**File:** `internal/queue/dispatcher.go`  
**Lines:** 309-312

**Issue:** The `fetch` function uses an inefficient busy-wait loop with `time.Sleep(100 * time.Millisecond)` when no workers are available, wasting CPU cycles.

**Current Code:**

```go
var workers int
for workers = len(d.availableWorkers); workers == 0; workers = len(d.availableWorkers) {
    time.Sleep(100 * time.Millisecond)  // Busy-wait wastes CPU
}
```

**Recommendation:** Use channel-based signaling:

```go
// Add a condition variable or use the existing availableWorkers channel
select {
case <-d.availableWorkers:
    // Worker available, proceed
case <-ctx.Done():
    return
}
```

**Impact:** Reduced CPU efficiency during queue saturation periods.

---

### 1.3 Potential Goroutine Leak in Health Check ⚠️ **MEDIUM**

**File:** `internal/health/health.go`  
**Lines:** 286-305

**Issue:** The `runDue` function spawns goroutines for each check but doesn't ensure completion if context is cancelled mid-loop.

**Current Code:**

```go
results := make(chan CheckResult, len(due))
for _, check := range due {
    go func(check Check) {
        results <- c.runOne(ctx, check)
    }(check)
}
```

**Recommendation:** Use `errgroup.Group` for proper goroutine lifecycle:

```go
g, ctx := errgroup.WithContext(ctx)
results := make(chan CheckResult, len(due))
for _, check := range due {
    check := check // capture loop variable
    g.Go(func() error {
        results <- c.runOne(ctx, check)
        return nil
    })
}
go func() {
    _ = g.Wait()  // Wait for all goroutines
    close(results)
}()
```

**Impact:** Potential goroutine leaks during rapid shutdowns.

---

### 1.4 Potential Goroutine Leak in Logger Shutdown ⚠️ **MEDIUM**

**File:** `internal/logger/logger.go`  
**Lines:** 272-288

**Issue:** The `closeSink` function spawns a goroutine to close the sink but doesn't handle cases where `Close()` might block indefinitely.

**Current Code:**

```go
done := make(chan error, 1)
go func() { done <- sink.Close() }()
select {
case err := <-done:
    // ...
case <-ctx.Done():
    // Goroutine may still be running
}
```

**Recommendation:** Add timeout and force cleanup:

```go
done := make(chan error, 1)
go func() { done <- sink.Close() }()
select {
case err := <-done:
    return err
case <-time.After(5 * time.Second):
    return fmt.Errorf("sink close timeout")
case <-ctx.Done():
    return ctx.Err()
}
```

**Impact:** Potential hangs during shutdown if sink Close() blocks.

---

## 2. MEMORY LEAKS & RESOURCE MANAGEMENT

### 2.1 Resource Cleanup ✅ **GOOD**

**Status:** No critical memory leaks found. The codebase consistently uses `defer` for cleanup:

- Database rows: `defer rows.Close()` in all repositories
- File handles: `defer f.Close()` in storage operations  
- HTTP responses: `defer resp.Body.Close()` in external calls
- Context cancellation: Properly propagated through call chains

**Files Reviewed:** All repository files, storage manager, fetcher, mailer

---

### 2.2 Buffer Pool Management ℹ️ **LOW**

**File:** `internal/queue/client.go`  
**Lines:** 286-297

**Issue:** Buffer pool may retain large buffers, but this is an acceptable trade-off for performance.

**Recommendation:** Consider adding maximum buffer size limits to the pool if memory becomes a concern.

---

## 3. SHADOW DECLARATIONS

### 3.1 Transaction Callback Pattern ✅ **ACCEPTABLE**

**Status:** Variable shadowing in transaction callbacks is intentional and follows the standard pattern:

```go
pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
    // tx parameter shadows outer scope - this is intentional
})
```

**Recommendation:** No action needed - this is the correct pattern for transaction scoping.

---

## 4. SECURITY VULNERABILITIES

### 4.1 SQL Injection ✅ **PROTECTED**

**Status:** No SQL injection vulnerabilities found. The codebase consistently uses:

- `go-sqlbuilder` for query construction
- Parameterized queries throughout
- Proper quoting in dynamic SQL (backup.go)

**Files Reviewed:** All repository files, database backup/restore

---

### 4.2 Timing Attack Protection ✅ **PROTECTED**

**File:** `modules/identity/multifactor/service.go`  
**Lines:** 271, 429-431

**Status:** Properly implemented using `subtle.ConstantTimeCompare` for:

- TOTP code verification
- Hash comparisons

**No action needed.**

---

### 4.3 Secret Logging ✅ **PROTECTED**

**Status:** Comprehensive redaction mechanism in place:

- `internal/config/redact.go` handles secret redaction
- No sensitive data in log statements
- Credentials never logged

**No action needed.**

---

### 4.4 Error Information Disclosure ℹ️ **LOW**

**File:** `internal/queue/dispatcher.go`  
**Line:** 318

**Issue:** Error messages may contain database structure information.

**Current Code:**

```go
d.client.log.ErrorContext(ctx, "queue: failed to claim tasks", "err", err.Error())
```

**Recommendation:** Consider sanitizing error messages in production environments or ensure logging layer handles redaction.

---

## 5. PERFORMANCE OPTIMIZATION OPPORTUNITIES

### 5.1 N+1 Query Pattern in Multi-Factor ℹ️ **MEDIUM**

**File:** `modules/identity/multifactor/signin_bridge.go`  
**Lines:** 162-189

**Issue:** Multiple decryption operations in a loop for users with many devices.

**Current Code:**

```go
confirmed, err := s.repo.ListTotp(ctx, db, userID)
for _, row := range confirmed {
    secret, err := s.unseal(row.Secret)  // Decrypt each secret
    // ...
}
```

**Recommendation:**

- Add database-level indexing on frequently queried fields
- Consider caching unsealed secrets if this becomes a bottleneck
- Monitor query performance for users with many devices

---

### 5.2 String Allocation in Cache ℹ️ **LOW**

**File:** `internal/cache/cache_memory.go`  
**Lines:** 272-274

**Issue:** Byte-to-string conversions create temporary allocations in hot path.

**Current Code:**

```go
return string(prefix) == key[:len(prefix)] && string(rest) == key[len(prefix):]
```

**Recommendation:** Use `bytes.Equal` for comparison to avoid allocations:

```go
return bytes.Equal(prefix, []byte(key[:len(prefix)))) && 
       bytes.Equal(rest, []byte(key[len(prefix):]))
```

---

### 5.3 Connection Pool Configuration ℹ️ **LOW**

**File:** `internal/datastore/postgres.go`  
**Lines:** 18-27

**Issue:** Default pool settings may not be optimal for high-traffic deployments.

**Current Defaults:**

```go
defaultMaxConns             = 10
defaultMinConns             = 2
defaultMaxConnLifetime      = time.Hour
defaultMaxConnIdleTime      = 30 * time.Minute
```

**Recommendation:**

- Make these configurable based on deployment size
- Document recommended values for different traffic levels
- Consider adding connection pool metrics

---

### 5.4 Channel Buffer Sizing ℹ️ **LOW**

**File:** `internal/queue/dispatcher.go`  
**Lines:** 126-127

**Issue:** Fixed buffer sizes may become bottlenecks under high load.

**Current Code:**

```go
d.ready = make(chan struct{}, 1000)
d.trigger = make(chan struct{}, 10)
```

**Recommendation:**

- Make buffer sizes configurable
- Add metrics to monitor channel depth
- Consider dynamic sizing based on load

---

## 6. POSITIVE FINDINGS ✅

The codebase demonstrates excellent practices in:

1. **Security:** Comprehensive timing attack protection, secret redaction, parameterized queries
2. **Resource Management:** Consistent use of defer for cleanup, proper context handling
3. **Error Handling:** Proper error wrapping and propagation throughout
4. **Transaction Management:** Proper scoping and rollback on error
5. **Concurrency:** Proper mutex usage in cache and most concurrent structures
6. **Code Quality:** Clean architecture, clear separation of concerns, comprehensive tests

---

## 7. RECOMMENDED ACTION PLAN

### Immediate (This Sprint)

1. **Fix watcher timer map race condition** - Add `sync.Mutex` to protect concurrent access
2. **Test the fix** - Add race detector tests to the watcher package

### Short-term (Next Sprint)

3. **Replace busy-wait loop** - Implement channel-based signaling in queue dispatcher
4. **Add goroutine lifecycle management** - Use `errgroup.Group` in health checks
5. **Add logger shutdown timeout** - Prevent indefinite hangs during shutdown

### Medium-term (Next Quarter)

6. **Performance monitoring** - Add metrics for channel depths, connection pool usage
7. **Configuration review** - Make performance-critical settings configurable
8. **Load testing** - Validate optimizations under production-like load

### Long-term (Ongoing)

9. **Security audit** - Regular review of error message sanitization
10. **Performance profiling** - Continuous monitoring and optimization of hot paths

---

## 8. TESTING RECOMMENDATIONS

### Race Detection

Run the following regularly:

```bash
go test -race ./...
```

### Memory Profiling

```bash
go test -memprofile=mem.prof ./...
go tool pprof mem.prof
```

### CPU Profiling

```bash
go test -cpuprofile=cpu.prof ./...
go tool pprof cpu.prof
```

---

## 9. CONCLUSION

The tango codebase is **well-engineered** with strong security practices and proper resource management. The identified issues are primarily related to **concurrency patterns** and **performance optimizations** rather than critical security vulnerabilities.

**Key Strengths:**

- No SQL injection vulnerabilities
- Proper timing attack protection
- Consistent resource cleanup
- Strong transaction management
- Comprehensive error handling

**Areas for Improvement:**

- Concurrency safety in file system watcher
- Goroutine lifecycle management
- Performance tuning for high-traffic scenarios

**Overall Assessment:** **PRODUCTION-READY** with recommended improvements for reliability and performance optimization.
