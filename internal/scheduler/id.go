package scheduler

import (
	"fmt"
	"uuid"

	"go.jetify.com/typeid"

	"github.com/riipandi/saka/pkg/strutils"
)

// JobIDPrefix is the TypeID prefix of a scheduler job's identifier. The id
// leaves the server in an API response, so the reader of a log line or a
// support ticket can tell it names a scheduled job without a lookup.
type JobIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (JobIDPrefix) Prefix() string { return "scd" }

// JobID is the typed identifier of one row of the scheduler_jobs table, in
// its wire form.
type JobID = typeid.TypeID[JobIDPrefix]

// IDFromUUID wraps the state row's UUID into the wire form. It is the one
// direction every response takes.
func IDFromUUID(raw uuid.UUID) (JobID, error) {
	return strutils.EncodeID[JobID](raw)
}

// FormatID renders the wire form of a row's UUID. Rows read from the
// database always carry a valid UUID, so the render cannot fail; an invalid
// one answers the empty string, which no consumer should mistake for an id.
func FormatID(raw uuid.UUID) string {
	return strutils.FormatID[JobID](raw)
}

// ParseID reads the wire form back. It is the boundary a request crosses:
// an identifier that arrives without the `scd_` prefix names nothing this
// server speaks about, and the caller refuses it as the not-found it is.
func ParseID(wire string) (JobID, error) {
	parsed, err := strutils.ParseID[JobID](wire)
	if err != nil {
		return JobID{}, fmt.Errorf("scheduler: %w", err)
	}
	return parsed, nil
}

// IDToUUID unwraps the wire form into the UUID the column stores. The typed
// id carries the bytes itself, so nothing re-parses text to get there.
func IDToUUID(id JobID) uuid.UUID {
	return strutils.ToUUID(id)
}

// UUIDFromWire is the request boundary in one step: the wire form a request
// carries in, the key the rows carry out. A malformed identifier names
// nothing, and the caller refuses it as the not-found it is.
func UUIDFromWire(wire string) (uuid.UUID, error) {
	return strutils.UUIDFromWire[JobID](wire)
}
