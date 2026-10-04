package usergroup

import (
	"fmt"
	"time"

	"uuid"

	"go.jetify.com/typeid"

	"github.com/riipandi/saka/pkg/strutils"
)

// ResourceGroup is the resource type an audit record names when the change is
// about a group. The record's user_id stays empty — a group is not an account
// — and the group is named in resource_type and resource_id, the way any other
// acted-on resource is.
const ResourceGroup = "user_group"

// GroupIDPrefix is the TypeID prefix of a group's identifier. The id leaves
// the server in an API response, so the reader of a log line or a support
// ticket can tell what it names without a lookup.
type GroupIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (GroupIDPrefix) Prefix() string { return "ugrp" }

// GroupID is the typed identifier of one row of entity.TableUserGroups, in its wire
// form. The column stays a UUID; the conversion is strutils', bound to this
// prefix by the type.
type GroupID = typeid.TypeID[GroupIDPrefix]

// IDFromUUID wraps the row's UUID into the wire form.
func IDFromUUID(raw uuid.UUID) (GroupID, error) {
	return strutils.EncodeID[GroupID](raw)
}

// FormatID renders the wire form of a row's UUID. Rows read from the
// database always carry a valid UUID, so the render cannot fail; an invalid
// one answers the empty string, which no consumer should mistake for an id.
func FormatID(raw uuid.UUID) string {
	return strutils.FormatID[GroupID](raw)
}

// ParseID reads the wire form back. It is the boundary a request crosses: an
// identifier that arrives without the prefix names no group, the not-found
// the caller refuses.
func ParseID(wire string) (GroupID, error) {
	parsed, err := strutils.ParseID[GroupID](wire)
	if err != nil {
		return GroupID{}, fmt.Errorf("usergroup: %w", err)
	}
	return parsed, nil
}

// IDToUUID unwraps the wire form into the UUID the column stores. The typed
// id carries the bytes itself, so nothing re-parses text to get there.
func IDToUUID(id GroupID) uuid.UUID {
	return strutils.ToUUID(id)
}

// UUIDFromWire is the request boundary in one step: the wire form a request
// carries in, the key the rows carry out.
func UUIDFromWire(wire string) (uuid.UUID, error) {
	return strutils.UUIDFromWire[GroupID](wire)
}

// GroupSchema is one row of entity.TableUserGroups. The updated column is nullable by
// construction: the trigger fills it on the first update, so a row never
// updated reads as nil, not as a zero instant.
type GroupSchema struct {
	ID          GroupID    `db:"id"`
	Name        string     `db:"name"`
	DisplayName string     `db:"display_name"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   *time.Time `db:"updated_at"`
}

// GroupRow is one row of a list answer: the group plus the member count the
// query computes. The count is not a column — it is what the LEFT JOIN counts,
// and a group with no members reads as zero because the join is left.
type GroupRow struct {
	GroupSchema
	UserCount int
}
