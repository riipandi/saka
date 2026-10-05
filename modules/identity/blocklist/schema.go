// Package blocklist is the sign-up blocklist: the identifiers no open-mode
// sign-up may claim while access.blocklist_enabled is on. The entries are
// rows an administrator manages through the BlocklistService; the gate that
// reads them sits in the features that enforce the lists — sign-up, and
// sign-in while its toggle says so — through the seams those features
// define.
//
// The address-shape helpers live here too: the grammar an entry must parse
// as, and the base forms the match compares. They are pure functions, so a
// consumer imports them directly — no service dependency, no cycle.
package blocklist

import (
	"time"

	"uuid"
)

// ResourceBlocklistEntry is the resource type an audit record names when the
// change is about an entry. The record's user_id names the administrator who
// made the change.
const ResourceBlocklistEntry = "blocklist_entry"

// EntrySchema is one row of entity.TableBlocklistEntries. The pattern is stored lowercased —
// the match is case-insensitive, so the row normalizes once at the write
// instead of at every read.
type EntrySchema struct {
	ID        uuid.UUID  `db:"id"`
	Pattern   string     `db:"pattern"`
	CreatedBy *uuid.UUID `db:"created_by"`
	CreatedAt time.Time  `db:"created_at"`
}
