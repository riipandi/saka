package health

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/dustin/go-humanize"

	"github.com/riipandi/saka/pkg/printext"
)

// InfoUptime is the key Uptime reports under.
const InfoUptime = "uptime"

// Uptime returns an info function that reports how long the process has been
// running, for WithInfoFunc.
//
// started is the process start time. Go exposes no portable process start time,
// so a caller records it once at startup and passes it here; the value is then
// rendered on every check, because uptime changes between calls.
func Uptime(started time.Time) func(context.Context) map[string]string {
	return func(context.Context) map[string]string {
		return map[string]string{InfoUptime: FormatUptime(time.Since(started))}
	}
}

// FormatUptime renders an uptime for a human. go-humanize has no sub-minute
// granularity, so a process younger than a minute would read as "now", which
// looks like a missing value rather than a short uptime.
func FormatUptime(elapsed time.Duration) string {
	if elapsed < time.Minute {
		return "<1 minute"
	}
	return humanize.RelTime(time.Now().Add(-elapsed), time.Now(), "", "")
}

// This file defines the wire form of a result. Both surfaces publish it: the
// CLI prints it under --json and the REST handler sends it as the response
// payload, so the two never drift.
//
// The output is deterministic, which a plain struct dump is not:
//
//   - Durations are milliseconds. time.Duration marshals as nanoseconds, which
//     is unreadable in a probe response.
//   - Details are an ordered array, not a map. A map has no order, so a diff of
//     two responses would churn and a human would read a different order each
//     time. The order is by check name.
//   - The info entries are flattened to the top level, sorted by key, so mode,
//     uptime, and version sit beside status instead of behind a nested object;
//     without sorting, two identical probes would produce byte-different
//     bodies.

// jsonCheckResult is the wire form of CheckResult.
type jsonCheckResult struct {
	Name      string    `json:"name"`
	Status    Status    `json:"status"`
	Target    string    `json:"target,omitempty"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	TookMS    float64   `json:"took_ms"`
	Optional  bool      `json:"optional,omitzero"`
}

// reservedInfoKeys are the wire fields an info entry must not shadow. The
// entries are flattened to the top level of the JSON form, so a key equal to
// one of these would write the same field twice.
var reservedInfoKeys = []string{"status", "details", "took_ms"}

// jsonResult is the fixed head of the wire form of Result. The info entries
// are appended after these fields by Result.MarshalJSON, which is what
// flattens them to the top level.
type jsonResult struct {
	Status  GlobalStatus      `json:"status"`
	Details []jsonCheckResult `json:"details"`
	TookMS  float64           `json:"took_ms"`
}

// MarshalJSON implements json.Marshaler.
func (r CheckResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.wire())
}

// MarshalJSON implements json.Marshaler. The info entries are flattened to
// the top level, sorted by key after the fixed fields, so a probe consumer
// reads mode, uptime, and version beside status rather than behind an
// "info" object.
func (r Result) MarshalJSON() ([]byte, error) {
	details := make([]jsonCheckResult, 0, len(r.Details))
	for _, name := range sortedNames(r.Details) {
		details = append(details, r.Details[name].wire())
	}

	head, err := json.Marshal(jsonResult{
		Status:  r.Status,
		Details: details,
		TookMS:  milliseconds(r.Duration),
	})
	if err != nil {
		return nil, err
	}

	// The head ends with the closing brace; the info entries are spliced in
	// before it, so the object stays one document.
	var out bytes.Buffer
	out.Write(head[:len(head)-1])
	for _, key := range sortedKeys(r.Info) {
		out.WriteByte(',')
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		encodedValue, err := json.Marshal(r.Info[key])
		if err != nil {
			return nil, err
		}
		out.Write(encodedKey)
		out.WriteByte(':')
		out.Write(encodedValue)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// wire converts a check result to its published form.
func (r CheckResult) wire() jsonCheckResult {
	return jsonCheckResult{
		Name:      r.Name,
		Status:    r.Status,
		Target:    r.Target,
		Error:     r.Error,
		Timestamp: r.Timestamp,
		TookMS:    milliseconds(r.Duration),
		Optional:  r.Optional,
	}
}

// milliseconds renders a duration as fractional milliseconds.
func milliseconds(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

// state holds what a Checker remembers between calls: the last result of each
// check, and the aggregated status last reported to a listener.
//
// It is separate from Checker so the locking rules live in one place: the
// result of every check is written by the goroutine that ran it and read by the
// caller that assembles the aggregate.
type state struct {
	mu sync.Mutex
	// results holds the last outcome per check name.
	results map[string]CheckResult
	// reportedStatus is the aggregated status last handed to a listener, and
	// hasReported distinguishes a real status from "never reported".
	reportedStatus GlobalStatus
	hasReported    bool
}

// newState returns an empty state sized for the configured checks.
func newState(checks int) state {
	return state{results: make(map[string]CheckResult, checks)}
}

// due returns the checks whose result is missing or older than ttl. A ttl of
// zero makes every check due, which is what disables the cache.
func (s *state) due(checks []Check, now time.Time, ttl time.Duration) []Check {
	s.mu.Lock()
	defer s.mu.Unlock()

	due := make([]Check, 0, len(checks))
	for _, check := range checks {
		result, ok := s.results[check.Name]
		if !ok || result.Timestamp.IsZero() || now.Sub(result.Timestamp) >= ttl {
			due = append(due, check)
		}
	}
	return due
}

// store records fresh outcomes, leaving every other entry untouched.
func (s *state) store(results map[string]CheckResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	maps.Copy(s.results, results)
}

// snapshot copies the current results, so the caller assembles the aggregate
// without holding the lock while it sorts and walks the map.
func (s *state) snapshot() map[string]CheckResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]CheckResult, len(s.results))
	maps.Copy(out, s.results)
	return out
}

// recordStatus stores the aggregated status and reports whether it differs from
// the one reported before. The first call is not a change: there is nothing to
// compare against yet.
func (s *state) recordStatus(status GlobalStatus) (changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed = s.hasReported && s.reportedStatus != status
	s.reportedStatus = status
	s.hasReported = true
	return changed
}

// statusStyle maps a status word to its meaning. Both vocabularies are covered,
// so the aggregate line and a component line are marked by the same rule. A word
// this build does not know is a warning rather than a failure: an unknown state
// is not the same as a broken one.
//
// The mapping lives here rather than in the rendering package because it is this
// package that decides what "up" and "healthy" mean. printext only decides how a
// meaning is painted.
func statusStyle(status string) printext.Style {
	switch status {
	case string(StatusUp), string(GlobalHealthy):
		return printext.StyleOK
	case string(StatusDown), string(GlobalUnhealthy):
		return printext.StyleFail
	default:
		return printext.StyleWarn
	}
}

// countStyle marks a "N down" count: no failures is a pass, any other count is a
// failure.
func countStyle(down int) printext.Style {
	if down == 0 {
		return printext.StyleOK
	}
	return printext.StyleFail
}

// WriteText writes a human-readable report of a result.
//
// The shape is a flat list, one fact per line, so it greps and pipes without
// column padding to strip:
//
//	name: saka
//	version: 0.0.0
//	uptime: 3 hours
//	status: healthy
//	duration: 1.234 ms
//	checks: 2 up, 0 down
//	postgres: up (localhost:5432/postgres)
//	storage: up (/srv/storage)
//
// Every line is "<key>: <value>", and a check line is
// "<name>: <status>[ optional][ (<target>)][: <error>]", one per check in
// check-name order. A failing check carries its error on the same line, so
// `grep ': down'` finds every problem. Every failing check is reported, not just
// the first.
//
// The styler marks each part; pass nil for plain text, which is what a
// redirected run and a test both want. The meaning of a part is decided here,
// next to the status vocabulary, and only its rendering is the caller's.
//
// Per-check durations and timestamps are deliberately absent: they are per-run
// numbers that answer no question a reader has. The JSON form keeps them for a
// machine that measures them.
func WriteText(w io.Writer, result Result, styler printext.Styler) error {
	if err := writeSummary(w, result, styler); err != nil {
		return err
	}
	if len(result.Details) == 0 {
		return nil
	}
	return writeDetails(w, result, styler)
}

// writeSummary writes the identity and aggregate lines.
func writeSummary(w io.Writer, result Result, styler printext.Styler) error {
	if err := writeInfo(w, result.Info, styler); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "%s: %s\n",
		printext.Mark(styler, printext.StyleLabel, "status"),
		printext.Mark(styler, statusStyle(string(result.Status)), string(result.Status))); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s: %s\n",
		printext.Mark(styler, printext.StyleLabel, "duration"),
		printext.Mark(styler, printext.StyleMuted, printext.Duration(result.Duration))); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "%s: %s\n",
		printext.Mark(styler, printext.StyleLabel, "checks"), checkCounts(result.Details, styler))
	return err
}

// writeInfo writes the info map as one "key: value" line per entry, in key
// order, so the output does not depend on map iteration order.
func writeInfo(w io.Writer, info map[string]string, styler printext.Styler) error {
	for _, key := range sortedKeys(info) {
		if _, err := fmt.Fprintf(w, "%s: %s\n",
			printext.Mark(styler, printext.StyleLabel, key), info[key]); err != nil {
			return err
		}
	}
	return nil
}

// writeDetails writes one line per check, in check-name order.
func writeDetails(w io.Writer, result Result, styler printext.Styler) error {
	for _, name := range sortedNames(result.Details) {
		if _, err := fmt.Fprintln(w, checkLine(result.Details[name], styler)); err != nil {
			return err
		}
	}
	return nil
}

// checkLine renders one check as a single line. Optional parts are omitted
// rather than left blank, so a healthy check stays short and a failure keeps
// everything needed to act on it.
func checkLine(detail CheckResult, styler printext.Styler) string {
	var line strings.Builder
	line.WriteString(printext.Mark(styler, printext.StyleLabel, detail.Name))
	line.WriteString(": ")
	line.WriteString(printext.Mark(styler, statusStyle(string(detail.Status)), string(detail.Status)))
	if detail.Optional {
		line.WriteString(printext.Mark(styler, printext.StyleWarn, " optional"))
	}
	if detail.Target != "" {
		line.WriteString(" (")
		line.WriteString(printext.Mark(styler, printext.StyleMuted, detail.Target))
		line.WriteString(")")
	}
	if detail.Error != "" {
		line.WriteString(": ")
		line.WriteString(printext.Mark(styler, printext.StyleFail, detail.Error))
	}
	return line.String()
}

// checkCounts summarizes the details as "1 up, 0 down", appending the optional
// count when there is one. Counts go through go-humanize so a large number stays
// readable.
//
// Only the "N down" count is marked: it is the number a reader acts on, and
// marking every number would leave nothing to stand out.
func checkCounts(details map[string]CheckResult, styler printext.Styler) string {
	var up, down, optional int
	for _, detail := range details {
		if detail.Optional {
			optional++
		}
		if detail.Status == StatusDown {
			down++
			continue
		}
		up++
	}

	summary := fmt.Sprintf("%s up, %s down",
		humanize.Comma(int64(up)),
		printext.Mark(styler, countStyle(down), humanize.Comma(int64(down))))
	if optional > 0 {
		summary += fmt.Sprintf(", %s optional", humanize.Comma(int64(optional)))
	}
	return summary
}

// WriteShort writes only the aggregated status, one word on one line, so a
// shell script or a probe can read it without parsing anything:
//
//	healthy
//
// It prints the same word the JSON status field carries.
func WriteShort(w io.Writer, result Result) error {
	_, err := fmt.Fprintln(w, result.Status)
	return err
}

// Message describes a result in one line for a log entry or an API message. It
// names the failing checks, because a bare status does not say what to look at.
func Message(result Result) string {
	failed := result.Failed()
	if len(failed) == 0 {
		return fmt.Sprintf("%s (%s)", result.Status, printext.Duration(result.Duration))
	}
	return fmt.Sprintf("%s: %s is down", result.Status, strings.Join(failed, ", "))
}
