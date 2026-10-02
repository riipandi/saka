// Package oauthsso is the sign-in-with-a-provider feature: the builtin
// Google and GitHub connections, the operator-configured custom OIDC ones,
// the outbound authorization-code flow they run, and the linked accounts
// they bind into accounts. The feature is tango-only — upstream Pocket ID
// never signed an account in through an external identity provider.
//
// The surface splits at the transport: the operator's connection CRUD and
// the holder's linked-account ledger are procedures, while the flow's start
// and callback are REST routes the browser crosses mid-redirect. The flow
// itself lives beside this package's service (phase 3); this package's root
// owns the connection model, the sealing, and the discovery validation the
// CRUD runs.
package oauthsso

import (
	"fmt"
	"time"

	"uuid"

	"go.jetify.com/typeid"
)

// The tables the feature's rows live in. The migrations own the schema;
// these constants are how Go code names them, so a rename touches one line.
const (
	connectionTable    = "public.oauth_connections"
	linkedAccountTable = "public.oauth_linked_accounts"
	flowTable          = "public.oauth_flows"
)

// ConnectionKind is what a connection's endpoints come from: a builtin
// kind's endpoints are the code's own (the provider slug must name one),
// a custom kind's ride a discovery document or manual endpoints.
type ConnectionKind string

const (
	KindBuiltin ConnectionKind = "builtin"
	KindCustom  ConnectionKind = "custom"
)

// ResourceOAuthConnection is the resource type an audit record names when
// the change is about a connection. The record's payload names the
// provider slug, because a reader of the log line matches on the word the
// surface addresses, not on the row's UUID.
const ResourceOAuthConnection = "oauth_connection"

// FlowStage is the position one authorization-code ceremony rests in. The
// stage machine is the flow's spine: a stage is left only by the write
// that spends it, and a caller may never skip one. Declared here so the
// schema's CHECK constraint and the flow code name the same words.
type FlowStage string

const (
	// StagePending is the flow the browser is out walking: the callback
	// has not resolved it yet.
	StagePending FlowStage = "pending"
	// StageVerifyEmail is the flow whose provider address the email code
	// must prove before anything links or is created.
	StageVerifyEmail FlowStage = "verify_email"
	// StageRequireNames is the flow whose provider answered no given and
	// family names; the continue call supplies them.
	StageRequireNames FlowStage = "require_names"
	// StageCompleted is a spent flow: the resolution answered, the row
	// remains only until the sweep collects it.
	StageCompleted FlowStage = "completed"
)

// ConnectionIDPrefix is the TypeID prefix of a connection row's identifier.
type ConnectionIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (ConnectionIDPrefix) Prefix() string { return "oconn" }

// ConnectionID is the typed identifier of one row of connectionTable, in
// its wire form. The column stays a UUID; the conversion lives here and
// nowhere else.
type ConnectionID = typeid.TypeID[ConnectionIDPrefix]

// LinkedAccountIDPrefix is the TypeID prefix of a linked account's
// identifier.
type LinkedAccountIDPrefix struct{}

// Prefix reports the TypeID prefix.
func (LinkedAccountIDPrefix) Prefix() string { return "olink" }

// LinkedAccountID is the typed identifier of one row of linkedAccountTable.
type LinkedAccountID = typeid.TypeID[LinkedAccountIDPrefix]

// IDFromUUID wraps the connection row's UUID into the wire form. It is
// the one direction every response takes.
func IDFromUUID(raw uuid.UUID) (ConnectionID, error) {
	return typeid.FromUUID[ConnectionID](raw.String())
}

// FormatID renders the wire form of a connection row's UUID. Rows read
// from the database always carry a valid UUID, so the render cannot fail;
// an invalid one answers the empty string, which no consumer should
// mistake for an id.
func FormatID(raw uuid.UUID) string {
	id, err := IDFromUUID(raw)
	if err != nil {
		return ""
	}
	return id.String()
}

// ParseID reads the connection's wire form back. It is the boundary a
// request crosses: an identifier that arrives without the prefix names no
// connection, the not-found the caller refuses.
func ParseID(wire string) (ConnectionID, error) {
	parsed, err := typeid.Parse[ConnectionID](wire)
	if err != nil {
		return ConnectionID{}, fmt.Errorf("oauthsso: %w", err)
	}
	return parsed, nil
}

// IDToUUID unwraps the wire form into the UUID the column stores. The
// typed id carries the bytes itself, so nothing re-parses text to get
// there.
func IDToUUID(id ConnectionID) uuid.UUID {
	return uuid.UUID(id.UUIDBytes())
}

// UUIDFromWire is the request boundary in one step: the wire form a
// request carries in, the key the rows carry out.
func UUIDFromWire(wire string) (uuid.UUID, error) {
	id, err := ParseID(wire)
	if err != nil {
		return uuid.Nil(), err
	}
	return IDToUUID(id), nil
}

// LinkedAccountIDFromUUID wraps a linked account row's UUID into the wire
// form.
func LinkedAccountIDFromUUID(raw uuid.UUID) (LinkedAccountID, error) {
	return typeid.FromUUID[LinkedAccountID](raw.String())
}

// FormatLinkedAccountID renders the wire form of a linked account row's
// UUID.
func FormatLinkedAccountID(raw uuid.UUID) string {
	id, err := LinkedAccountIDFromUUID(raw)
	if err != nil {
		return ""
	}
	return id.String()
}

// ParseLinkedAccountID reads the linked account's wire form back.
func ParseLinkedAccountID(wire string) (uuid.UUID, error) {
	parsed, err := typeid.Parse[LinkedAccountID](wire)
	if err != nil {
		return uuid.Nil(), fmt.Errorf("oauthsso: %w", err)
	}
	return uuid.UUID(parsed.UUIDBytes()), nil
}

// Endpoints are the provider endpoints one connection talks to: the
// authorization and token endpoints the flow needs, and the userinfo and
// key-set endpoints the identity resolution reads. A builtin connection
// carries them in code; a custom one either resolved them from its
// discovery document or carries the operator's manual set.
type Endpoints struct {
	Authorization string
	Token         string
	Userinfo      string
	Jwks          string
}

// AttributeMapping names which provider claims answer the account's
// fields on a custom connection. An empty field keeps the default claim
// name — the mapping only ever widens what the provider's document says,
// never the account model.
type AttributeMapping struct {
	Email      string
	GivenName  string
	FamilyName string
}

// Connection is one row of connectionTable. The client secret is carried
// exactly as the row stores it — sealed, with the enc: prefix — and is
// unsealed only on the path that presents it to a provider. Every read
// the surface serves drops the field.
type Connection struct {
	ID               uuid.UUID
	Kind             ConnectionKind
	Provider         string
	DisplayName      string
	DiscoveryURL     string
	Endpoints        Endpoints
	ClientID         string
	ClientSecret     string
	Scopes           []string
	AttributeMapping AttributeMapping
	Enabled          bool
	CreatedAt        time.Time
	UpdatedAt        *time.Time
}

// BuiltIn reports whether the connection's endpoints are the code's own.
func (c Connection) BuiltIn() bool { return c.Kind == KindBuiltin }
