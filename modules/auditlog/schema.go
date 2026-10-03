package auditlog

import (
	"time"

	"github.com/riipandi/saka/internal/audit"
)

// Table is the table records live in. The migration owns the schema; this is
// how Go code names it.
//
// It is the same constant the writer uses, because a reader and a writer that
// disagreed about the table name would be a defect nothing catches until a
// query ran.
const Table = audit.Table

// The columns a record is read back through, in scan order.
//
// The nullable ones are read through a cast rather than a pointer: an absent
// account is the empty string a client sees, and the cast is where that is
// decided. `payload` is read as text and decoded by the caller, so the
// decoder is the one this repository names rather than the driver's.
// Every column is qualified with the record table's alias: the list joins the
// account table, and a bare `id` or `created_at` would be ambiguous the moment
// it does.
var columns = []string{
	"a.id::text",
	"a.created_at",
	"a.event",
	"a.trigger_type::text",
	"a.action_status::text",
	"COALESCE(a.user_id::text, '')",
	"COALESCE(u.username, '')",
	// host() rather than a cast: an INET cast renders the network mask
	// (`203.0.113.7/32`), which is not what a client asked for and not what
	// the address is. host() answers the address alone, and NULL for a
	// column that holds nothing.
	"COALESCE(host(a.ip_address), '')",
	"COALESCE(a.user_agent, '')",
	"COALESCE(a.device_fingerprint, '')",
	"COALESCE(a.country, '')",
	"COALESCE(a.city, '')",
	"COALESCE(a.resource_type, '')",
	"COALESCE(a.resource_id::text, '')",
	"a.payload::text",
}

// Row is one record as it is read back. The actor is not a column: it lives in
// the payload, because it describes the action rather than the account the
// action is about.
type Row struct {
	ID           string
	CreatedAt    time.Time
	Event        string
	TriggerType  string
	ActionStatus string
	UserID       string
	// Username is the account's name, read beside its identifier so a reader
	// does not have to resolve one to show the other. It is empty for a
	// record whose account no longer exists.
	Username     string
	IPAddress    string
	UserAgent    string
	Fingerprint  string
	Country      string
	City         string
	ResourceType string
	ResourceID   string
	// PayloadJSON is the payload as the database stored it. The service
	// decodes it, so this layer never has to know the payload's shape.
	PayloadJSON string
}

// UserOption is one account a filter can select.
type UserOption struct {
	ID       string
	Username string
}
