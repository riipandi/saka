package queue

import (
	"fmt"

	"go.jetify.com/typeid"
	"uuid"
)

// TaskIDPrefix is the TypeID prefix of a task's identifier. The id leaves
// the server in an API response, so the reader of a log line or a support
// ticket can tell it names a queue task without a lookup.
type TaskIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (TaskIDPrefix) Prefix() string { return "que" }

// TaskID is the typed identifier of one row of the pending or completed
// table, in its wire form.
type TaskID = typeid.TypeID[TaskIDPrefix]

// IDFromUUID wraps the row's UUID into the wire form. It is the one
// direction every response takes.
func IDFromUUID(raw uuid.UUID) (TaskID, error) {
	return typeid.FromUUID[TaskID](raw.String())
}

// FormatID renders the wire form of a row's UUID. Rows read from the
// database always carry a valid UUID, so the render cannot fail; an invalid
// one answers the empty string, which no consumer should mistake for an id.
func FormatID(raw uuid.UUID) string {
	id, err := IDFromUUID(raw)
	if err != nil {
		return ""
	}
	return id.String()
}

// ParseID reads the wire form back. It is the boundary a request crosses:
// an identifier that arrives without the `que_` prefix names nothing this
// server speaks about, and the caller refuses it as the not-found it is.
func ParseID(wire string) (TaskID, error) {
	parsed, err := typeid.Parse[TaskID](wire)
	if err != nil {
		return TaskID{}, fmt.Errorf("queue: %w", err)
	}
	return parsed, nil
}

// IDToUUID unwraps the wire form into the UUID the column stores. The typed
// id carries the bytes itself, so nothing re-parses text to get there.
func IDToUUID(id TaskID) uuid.UUID {
	return uuid.UUID(id.UUIDBytes())
}

// UUIDFromWire is the request boundary in one step: the wire form a request
// carries in, the key the rows carry out. A malformed identifier names
// nothing, and the caller refuses it as the not-found it is.
func UUIDFromWire(wire string) (uuid.UUID, error) {
	id, err := ParseID(wire)
	if err != nil {
		return uuid.Nil(), err
	}
	return IDToUUID(id), nil
}
