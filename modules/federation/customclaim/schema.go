package customclaim

import (
	"errors"
	"fmt"
	"time"
	"uuid"

	"go.jetify.com/typeid"
)

// ClaimTable is the custom claims table. The migrations own the schema; this
// constant is how Go code names it, so a table rename touches one line.
const ClaimTable = "public.custom_claims"

// ResourceCustomClaim is the resource type an audit record names when the
// change is about a claim.
const ResourceCustomClaim = "custom_claim"

// ClaimIDPrefix is the TypeID prefix of a claim row's identifier. The id
// leaves the server in an API response, so the reader of a log line can
// tell what it names without a lookup.
type ClaimIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (ClaimIDPrefix) Prefix() string { return "cclm" }

// ClaimID is the typed identifier of one row of ClaimTable, in its wire
// form. The column stays a UUID; the conversion lives here and nowhere else.
type ClaimID = typeid.TypeID[ClaimIDPrefix]

// IDFromUUID wraps the row's UUID into the wire form.
func IDFromUUID(raw uuid.UUID) (ClaimID, error) {
	return typeid.FromUUID[ClaimID](raw.String())
}

// FormatID renders the wire form. The rows the database always carry a valid
// UUID, so the render cannot fail; an invalid one answers the empty string,
// which no consumer should mistake for an id.
func FormatID(raw uuid.UUID) string {
	id, err := IDFromUUID(raw)
	if err != nil {
		return ""
	}
	return id.String()
}

// ParseID reads the wire form back. It is the boundary a request crosses: an
// identifier that arrives without the prefix names no claim, the not-found
// the caller refuses.
func ParseID(wire string) (ClaimID, error) {
	parsed, err := typeid.Parse[ClaimID](wire)
	if err != nil {
		return ClaimID{}, fmt.Errorf("customclaim: %w", err)
	}
	return parsed, nil
}

// IDToUUID unwraps the wire form into the UUID the column stores. The typed
// id carries the bytes itself, so nothing re-parses text to get there.
func IDToUUID(id ClaimID) uuid.UUID {
	return uuid.UUID(id.UUIDBytes())
}

// UUIDFromWire is the request boundary in one step: the wire form a request
// carries in, the key the rows carry out.
func UUIDFromWire(wire string) (uuid.UUID, error) {
	id, err := ParseID(wire)
	if err != nil {
		return uuid.Nil(), err
	}
	return IDToUUID(id), nil
}

// SuggestedKey is one key in the autocomplete list.
type SuggestedKey struct {
	Key        string
	UsageCount int64
}

// The failures the claim procedures report. The handler maps them to
// connect codes, the way every feature's failures are mapped.
var (
	// ErrClaimNotFound is a request whose identifier names no claim. A
	// malformed identifier answers it too: the shape is the not-found one
	// either way.
	ErrClaimNotFound = errors.New("customclaim: the claim does not exist")

	// ErrClaimExists is a creation whose (key, subject) pair another claim
	// holds — the unique index's answer, read from the write's failure.
	ErrClaimExists = errors.New("customclaim: the subject already carries this key")

	// ErrSubjectNotFound is a claim creation whose account or group names
	// no row — the foreign key's answer, mapped so the caller learns which
	// half was wrong.
	ErrSubjectNotFound = errors.New("customclaim: the named subject does not exist")

	// ErrClaimWrongSubject is a per-subject procedure whose identifier
	// names a claim of the other kind — a group claim offered to the user
	// surface, or the reverse. The two surfaces answer the same table, and
	// each refuses what belongs to the other.
	ErrClaimWrongSubject = errors.New("customclaim: the claim belongs to the other subject kind")
)

// ClaimSchema is one row of ClaimTable. Exactly one of UserID and
// GroupID is set — the schema's check constraint holds the pair — and the
// unique index on (key, user_id, user_group_id) is the storage of the
// per-subject key rule: NULLS NOT DISTINCT makes one NULL behave like a
// value, so the same key cannot attach to one subject twice.
type ClaimSchema struct {
	ID        uuid.UUID  `db:"id"`
	Key       string     `db:"key"`
	Value     string     `db:"value"`
	UserID    *uuid.UUID `db:"user_id"`
	GroupID   *uuid.UUID `db:"group_id"`
	CreatedAt time.Time  `db:"created_at"`
}

// ClaimView is a claim as the procedures answer it.
type ClaimView struct {
	ID    string
	Key   string
	Value string
}

// view renders the row in its wire form.
func (c ClaimSchema) view() ClaimView {
	return ClaimView{
		ID:    FormatID(c.ID),
		Key:   c.Key,
		Value: c.Value,
	}
}

// Claim is the fact the token-issuance seam carries: the key and the value,
// nothing else — the tokens never learn the row's identifiers.
type Claim struct {
	Key   string
	Value string
}
