package signin

import (
	"time"

	"uuid"

	"go.jetify.com/typeid"
)

// ProviderPassword is the `provider` value a session row carries when the
// credential that created it was the account's password.
const ProviderPassword = "password"

// ProviderOneTimeAccess is the `provider` value a session row carries when a
// one-time access code created it. The values this column takes are the
// vocabulary of how a session was opened, so they are named beside each other
// here rather than beside the features that mint them.
const ProviderOneTimeAccess = "one_time_access"

// ProviderTOTP is the `provider` value a session row carries when the
// sign-in ran past a second factor: the password was verified, and a TOTP
// code or a recovery code opened the session through the MFA bridge.
const ProviderTOTP = "totp"

// ProviderWebauthn is the `provider` value a session row carries when a
// passkey assertion with user verification opened the session whole — the
// one credential in the role of the full first-and-second factor.
const ProviderWebauthn = "webauthn"

// ProviderOAuthSSO is the `provider` value a session row carries when a
// single-sign-on provider answered the first factor: the flow's resolved
// identity bound to the account and opened the session through the OAuth
// bridge.
const ProviderOAuthSSO = "oauth_sso"

// KnownDeviceID is the typed identifier of one row of entity.TableKnownDevices. The
// identifier never leaves the server: the notice carries the device's
// fingerprint and the session's address, not this row's id.
type KnownDeviceID = typeid.TypeID[KnownDevicePrefix]

// KnownDevicePrefix is the TypeID prefix of a known device row.
type KnownDevicePrefix struct{}

// Prefix reports the TypeID prefix.
func (KnownDevicePrefix) Prefix() string { return "kdev" }

// KnownDeviceSchema is one row of entity.TableKnownDevices. LastSeenAt rides along so
// the row says when its fingerprint was last presented; the notice decision
// reads only whether the insert inserted.
type KnownDeviceSchema struct {
	ID                KnownDeviceID `db:"id"`
	UserID            uuid.UUID     `db:"user_id"`
	DeviceFingerprint string        `db:"device_fingerprint"`
	FirstSeenAt       time.Time     `db:"first_seen_at"`
	LastSeenAt        time.Time     `db:"last_seen_at"`
}
