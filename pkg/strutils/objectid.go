// Package strutils holds the string conversions the application repeats:
// the encodings a value takes on the way out, and the parses that bring it
// back.
//
// objectid.go holds the TypeID conversions every module's schema.go repeats:
// a row's UUID in, its typed wire form out, and back. The typed identifier —
// the `type XID = typeid.TypeID[XPrefix]` alias each module defines — stays
// the boundary: these helpers are generic over it, so a prefix is bound at
// compile time and a wire form carrying the wrong prefix is refused where it
// is read, not wherever the value is used.
package strutils

import (
	"uuid"

	"go.jetify.com/typeid"
)

// EncodeID wraps a row's UUID into its typed identifier, the wire form the
// API answers with. T is the module's TypeID alias, so the prefix the wire
// form carries is the one the type names, not a value the caller passes.
func EncodeID[T typeid.Subtype, PT typeid.SubtypePtr[T]](raw uuid.UUID) (T, error) {
	return typeid.FromUUIDBytes[T, PT](raw[:])
}

// FormatID renders the wire form of a row's UUID. Rows read from the
// database always carry a valid UUID, so the render cannot fail; an invalid
// one answers the empty string, which no consumer should mistake for an id.
func FormatID[T typeid.Subtype, PT typeid.SubtypePtr[T]](raw uuid.UUID) string {
	id, err := EncodeID[T, PT](raw)
	if err != nil {
		return ""
	}
	return id.String()
}

// ParseID reads the wire form back into its typed identifier. It is the
// boundary a request crosses: an identifier whose prefix is not the one T
// names is refused here, not wherever the value is used.
func ParseID[T typeid.Subtype, PT typeid.SubtypePtr[T]](wire string) (T, error) {
	return typeid.Parse[T, PT](wire)
}

// ToUUID unwraps a typed identifier into the UUID the column stores. The
// typed id carries the bytes itself, so nothing re-parses text to get there.
func ToUUID[T typeid.Subtype](id T) uuid.UUID {
	return uuid.UUID(id.UUIDBytes())
}

// UUIDFromWire is the request boundary in one step: the wire form a request
// carries in, the key the rows carry out. A malformed identifier answers
// uuid.Nil() beside the error, so a caller that only checks the error never
// stores a half-parsed key.
func UUIDFromWire[T typeid.Subtype, PT typeid.SubtypePtr[T]](wire string) (uuid.UUID, error) {
	id, err := ParseID[T, PT](wire)
	if err != nil {
		return uuid.Nil(), err
	}
	return ToUUID(id), nil
}
