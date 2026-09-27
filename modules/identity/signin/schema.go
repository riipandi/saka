package signin

import (
	"time"

	"go.jetify.com/typeid"
	"uuid"
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

// KnownDeviceTable is the table remembering every browser fingerprint an
// account has signed in from. A session row is a poor record of a device —
// it expires and gets cleaned up — so the first-seen judgement needs a row
// that outlives the sessions.
const KnownDeviceTable = "public.known_devices"

// KnownDeviceID is the typed identifier of one row of KnownDeviceTable. The
// identifier never leaves the server: the notice carries the device's
// fingerprint and the session's address, not this row's id.
type KnownDeviceID = typeid.TypeID[KnownDevicePrefix]

// KnownDevicePrefix is the TypeID prefix of a known device row.
type KnownDevicePrefix struct{}

// Prefix reports the TypeID prefix.
func (KnownDevicePrefix) Prefix() string { return "kdev" }

// KnownDeviceSchema is one row of KnownDeviceTable. LastSeenAt rides along so
// the row says when its fingerprint was last presented; the notice decision
// reads only whether the insert inserted.
type KnownDeviceSchema struct {
	ID                KnownDeviceID `db:"id"`
	UserID            uuid.UUID     `db:"user_id"`
	DeviceFingerprint string        `db:"device_fingerprint"`
	FirstSeenAt       time.Time     `db:"first_seen_at"`
	LastSeenAt        time.Time     `db:"last_seen_at"`
}
