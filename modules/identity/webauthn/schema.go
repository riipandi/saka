package webauthn

import (
	"time"

	"uuid"

	"go.jetify.com/typeid"
)

// ChallengeTypeRegistration and ChallengeTypeAuthentication name the two
// ceremony kinds the sessions table's check constraint admits.
const (
	ChallengeTypeRegistration   = "registration"
	ChallengeTypeAuthentication = "authentication"
)

// UserVerification values mirror the WebAuthn requirement levels. `required`
// is the only level whose assertion satisfies an MFA bridge, because
// presence alone does not prove the account holder is at the device.
const (
	UserVerificationRequired    = "required"
	UserVerificationPreferred   = "preferred"
	UserVerificationDiscouraged = "discouraged"
)

// CredentialPrefix is the TypeID prefix of an enrolled passkey's identifier.
// The id leaves the server in API responses, so a support ticket can tell
// which credential the reader is looking at without a lookup.
type CredentialPrefix struct{}

// Prefix reports the TypeID prefix.
func (CredentialPrefix) Prefix() string { return "psk" }

// CredentialID is the typed identifier of one row of entity.TableWebauthnCredentials.
type CredentialID = typeid.TypeID[CredentialPrefix]

// SessionPrefix is the TypeID prefix of a live ceremony's handle. The client
// echoes it between the begin and verify calls; it is never stored anywhere
// but this table, so a leak names nothing outside it.
type SessionPrefix struct{}

// Prefix reports the TypeID prefix.
func (SessionPrefix) Prefix() string { return "wcs" }

// SessionID is the typed identifier of one row of entity.TableWebauthnSessions.
type SessionID = typeid.TypeID[SessionPrefix]

// IDFromUUID wraps a row's UUID into the credential's wire form. It is the
// one direction every response takes.
func IDFromUUID(raw uuid.UUID) (CredentialID, error) {
	return typeid.FromUUID[CredentialID](raw.String())
}

// SessionIDFromUUID wraps a row's UUID into the ceremony handle's wire form.
func SessionIDFromUUID(raw uuid.UUID) (SessionID, error) {
	return typeid.FromUUID[SessionID](raw.String())
}

// CredentialSchema is one enrolled passkey. It lists only the columns the
// application writes; the db tags are the column names the query builder uses.
type CredentialSchema struct {
	ID           uuid.UUID `db:"id"`
	UserID       uuid.UUID `db:"user_id"`
	Name         string    `db:"name"`
	CredentialID []byte    `db:"credential_id"`
	PublicKey    []byte    `db:"public_key"`
	// SignCount is the signature counter the authenticator reported at its
	// last assertion. A regression between two assertions is the clone
	// signal, so the count is persisted even though upstream does not.
	SignCount int64 `db:"sign_count"`
	// AttestationType is the parsed format name ("none", "packed", ...). It
	// is recorded, not policy-enforced.
	AttestationType string `db:"attestation_type"`
	// Transport is the JSONB array of transport strings the authenticator
	// reported. JSONB travels as bytes here; an empty set is a real state.
	Transport []byte `db:"transport"`
	// BackupEligible says the credential can live in a synced passkey
	// provider; BackupState says it currently is. The
	// webauthn.allow_synced_passkeys setting is judged against eligibility
	// at enrollment; the state rides every assertion afterward.
	BackupEligible bool `db:"backup_eligible"`
	BackupState    bool `db:"backup_state"`
	// AAGUID is the authenticator model identifier, nil when the attestation
	// carries none. It names the fallback display name, nothing more.
	AAGUID     *string    `db:"aaguid"`
	CreatedAt  time.Time  `db:"created_at"`
	UpdatedAt  *time.Time `db:"updated_at"`
	LastUsedAt *time.Time `db:"last_used_at"`
}

// SessionSchema is one live ceremony row. Registration sessions always name
// their account; authentication sessions may not, because usernameless
// sign-in resolves the account from the credential.
type SessionSchema struct {
	ID               uuid.UUID  `db:"id"`
	UserID           *uuid.UUID `db:"user_id"`
	Challenge        string     `db:"challenge"`
	ChallengeType    string     `db:"challenge_type"`
	UserVerification string     `db:"user_verification"`
	// CredentialParams is the JSONB the go-webauthn session's credential
	// parameters marshal to. The service owns the mapping; the repository
	// carries bytes only.
	CredentialParams []byte `db:"credential_params"`
	// Extensions is the JSONB the ceremony's extension data marshals to.
	Extensions []byte    `db:"extensions"`
	CreatedAt  time.Time `db:"created_at"`
	ExpiresAt  time.Time `db:"expires_at"`
}
