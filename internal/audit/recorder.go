package audit

import (
	"context"
	"encoding/json/v2"
	"log/slog"
	"maps"
	"net/netip"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// The payload keys this package writes itself, beside whatever a feature
// supplies. They are part of the record's vocabulary, so the reader that
// lifts them back out into fields of its own reads the same constants.
const (
	// PayloadActorID and PayloadActorUsername name the administrator behind a
	// delegated action. The reader in modules/auditlog reads them under these
	// names, which is why they are exported rather than local.
	PayloadActorID       = "actor_id"
	PayloadActorUsername = "actor_username"
)

// Entry is one record to write. Every field but the event is optional: a
// record written before a caller exists — a sign-in that failed, a sign-up —
// names no account, and a client that sends no fingerprint names none.
//
// The zero value is not writable: a record with no event would be a row no
// reader can interpret, so the writer refuses it.
type Entry struct {
	// Event is what happened, from the constants above.
	Event string
	// Trigger is who caused it: a caller, or the application itself.
	Trigger string
	// Status is the outcome: completed, or refused.
	Status string
	// UserID is the account the action is about. It is the account that was
	// acted on, never the administrator who acted — the actor is the
	// payload's, so a delegated record keeps both. Empty means no account.
	UserID string
	// ResourceType and ResourceID name what was acted on, when that is not
	// the account itself.
	ResourceType string
	ResourceID   string
	// Payload is what the event needs to be read later: the fields a change
	// touched, the actor behind a delegated action, the reason a refusal
	// gave. It is written as JSONB and may be nil.
	Payload map[string]string
	// Client is where the request came from. Its fields are optional
	// individually, which is the state a request without a User-Agent or a
	// frontend that sends no fingerprint is in.
	Client ClientInfo
}

// Recorder writes audit records.
//
// It is the one writer, so a feature never builds an insert of its own: the
// table's columns, the enum values, and the shape of the payload are decided
// here, and a feature supplies only what happened.
//
// A record is written through the caller's query surface, which is what makes
// a record part of the transaction that caused it. An account deleted and the
// record of the deletion commit together or not at all, so the log cannot
// describe a change that was rolled back — the property a reader trusts it
// for.
type Recorder struct {
	log *slog.Logger
	// sink is the second side effect a record carries, wired after
	// construction. It is optional — an absent one is every deployment that
	// serves no webhook area — and its failure is the sink's to contain.
	sink Sink
}

// Sink is the seam one more side effect of a record rides: the emission that
// turns every record into a candidate webhook delivery. It receives the
// query surface the record rode, so a sink that writes rows joins the same
// transaction the change committed in — a rollback takes the emission with
// it, the property the record itself relies on.
//
// The interface lives here rather than in the webhook package because the
// recorder cannot know its consumers: a feature writes a record and moves
// on, and who else reads the record is the wiring's business, not the
// writer's.
type Sink interface {
	// AuditRecorded receives one written record, beside the query surface it
	// rode. It must never fail the caller: an emission is a side effect of
	// the change the caller asked for, not a condition of it.
	AuditRecorded(ctx context.Context, db datastore.Querier, entry Entry)
}

// WithSink arms the recorder's second side effect. It is wired after
// construction because the sink's own provider may resolve services the
// recorder must not depend on to construct.
func (r *Recorder) WithSink(sink Sink) *Recorder {
	r.sink = sink
	return r
}

// NewRecorder builds the recorder. A nil logger discards, which is the state
// a test that reads only rows is in.
func NewRecorder(log *slog.Logger) *Recorder {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Recorder{log: log}
}

// Record writes one record through the query surface the caller is already
// using: the pool, or the transaction the change runs in.
//
// It fills in the two things that come from the request rather than from the
// action, so a caller supplies only what happened:
//
//   - the client facts, when the entry carries none. They are what the
//     transport captured for this request (audit.ClientFromContext), which is
//     why a service never threads an address, an agent, or a fingerprint
//     through its own signature.
//   - the actor, when the caller is impersonating. The delegation is read
//     from the token rather than passed in, so every record written while one
//     account acts as another names the administrator behind it without the
//     feature that writes it knowing delegation exists.
//
// It never fails the caller. A record is a description of an action that has
// already happened, so refusing the action because its description could not
// be written would trade the thing the caller asked for against the thing
// that documents it. The failure is logged instead, with the event, so a
// broken audit trail is visible without being fatal.
//
// The insert runs under a savepoint (datastore.WithSavepoint). A failed
// statement poisons its transaction — every later statement and the commit
// fail with `25P02` — so ignoring the error on a bare insert would abort the
// caller's change along with the record, and report a failure that names
// neither. The savepoint contains the damage: the record is lost, the change
// the caller asked for commits.
//
// The signature takes the query surface rather than reaching for the pool,
// which is the whole reason a record can join the caller's transaction.
func (r *Recorder) Record(ctx context.Context, db datastore.Querier, entry Entry) {
	// A service built without a recorder writes no records. That is the state
	// a unit test of a feature is in, and it is answered here rather than by
	// a nil check at every call site: recording is a side effect, and a
	// feature that does not have one must still run.
	if r == nil {
		return
	}
	if entry.Event == "" {
		r.log.ErrorContext(ctx, "audit: record refused", "reason", "no event")
		return
	}
	if entry.Client == (ClientInfo{}) {
		entry.Client = ClientFromContext(ctx)
	}
	entry.Payload = withActor(ctx, entry.Payload)

	err := datastore.WithSavepoint(ctx, db, func(ctx context.Context, q datastore.Querier) error {
		ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
		ib.InsertInto(entity.TableAuditLogs)
		ib.Cols(
			"event", "trigger_type", "action_status", "payload",
			"ip_address", "user_agent", "device_fingerprint",
			"country", "city", "resource_type", "resource_id", "user_id",
		)
		ib.Values(
			entry.Event,
			trigger(entry.Trigger),
			status(entry.Status),
			payloadJSON(entry.Payload),
			addr(entry.Client.IPAddress),
			entry.Client.UserAgent,
			entry.Client.Fingerprint,
			entry.Client.Country,
			entry.Client.City,
			entry.ResourceType,
			nullableUUID(entry.ResourceID),
			nullableUUID(entry.UserID),
		)

		query, args := ib.Build()
		_, execErr := q.Exec(ctx, query, args...)
		return execErr
	})
	if err != nil {
		// The event is the one field that identifies which action went
		// unrecorded, so it is what the line carries.
		r.log.ErrorContext(ctx, "audit: record not written",
			"event", entry.Event, "err", err)
	}

	// The record is written; the emission rides the same surface whether the
	// row did or not — the happening is real either way, and the sink holds
	// its own savepoint, so a failed emission cannot poison the caller's
	// change.
	if r.sink != nil {
		r.sink.AuditRecorded(ctx, db, entry)
	}
}

// withActor records the administrator behind a delegated action.
//
// The delegation is read from the token the request carried rather than passed
// in, so a feature that writes a record while impersonation is in play names
// the actor without knowing delegation exists. The subject stays the account
// the action is about: a reader asking "whose activity is this" and a reader
// asking "who really did it" are two questions, and the record answers both.
//
// A caller that is not impersonating adds nothing, so an ordinary record
// carries no actor key at all rather than an empty one.
func withActor(ctx context.Context, payload map[string]string) map[string]string {
	caller, ok := jwtutils.CallerFrom(ctx)
	if !ok || !caller.IsImpersonating() {
		return payload
	}
	merged := make(map[string]string, len(payload)+2)
	maps.Copy(merged, payload)
	merged[PayloadActorID] = caller.ActorID
	merged[PayloadActorUsername] = caller.ActorUsername
	return merged
}

// trigger answers the enum value the column accepts. An unset trigger is the
// caller's, because every record written today is caused by a request; a
// writer that means the application itself says so.
func trigger(value string) string {
	if value == "" {
		return TriggerUser
	}
	return value
}

// status answers the enum value the column accepts. An unset status is a
// success, because a record is written after the action it describes: a
// refusal is the case a writer has to name.
func status(value string) string {
	if value == "" {
		return StatusSuccess
	}
	return value
}

// addr answers the value the INET column accepts. An address that cannot be
// parsed is stored as no address rather than refused: the record is worth
// more than the column, and a proxy header a caller controls is not a reason
// to lose it.
func addr(value string) *netip.Addr {
	if value == "" {
		return nil
	}
	parsed, err := netip.ParseAddr(value)
	if err != nil {
		return nil
	}
	return &parsed
}

// payloadJSON answers the value the JSONB column accepts. A nil payload is an
// empty object, the column's own default, so a reader never has to tell null
// from empty.
func payloadJSON(payload map[string]string) string {
	if len(payload) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		// A map of strings cannot fail to marshal, so this guards the
		// encoding rather than a caller's data.
		return "{}"
	}
	return string(encoded)
}

// nullableUUID answers the value a UUID column accepts: the identifier when
// there is one, and NULL when there is not. An empty string is not a valid
// UUID, so it cannot be passed through as itself.
func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}
