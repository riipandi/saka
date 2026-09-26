// Package audit is the vocabulary an audit record is written in, and the
// recorder that writes it.
//
// It is infrastructure rather than a module: a record is written from the
// request that caused it, in the same transaction, so every feature that
// changes something reaches the recorder the way it reaches the pool — by
// invoking it from the container. The module that *reads* records is
// modules/auditlog; this package never serves a request.
//
// The split is the same one internal/queue keeps: the vocabulary and the
// writer are shared, the surface that exposes them belongs to a module.
package audit

// The event names a record carries. They are this application's own spelling,
// snake_case like every other field it writes, rather than upstream's
// SCREAMING_SNAKE: a client reads `sign_in` beside `status_code`, and the two
// vocabularies would be one more thing to translate.
//
// An event names what happened, not which procedure answered. `user_created`
// is written by both the administrator's CreateUser and the sign-up flow,
// because a reader asking "when was this account created" wants one answer.
//
// TODO(audit): sign-out has no event here because it has no procedure — the
// session feature is a scaffold, and an event for a path nothing can take
// would be a name no writer reaches.
const (
	// EventSignIn is a credential verified and a session opened.
	EventSignIn = "sign_in"

	// EventAccountCreated is an account that did not exist now does. Sign-up
	// and the administrator's own creation both write it.
	EventAccountCreated = "account_created"

	// EventAccountUpdated is an account's fields rewritten.
	EventAccountUpdated = "account_updated"

	// EventAccountDeleted is an account removed.
	EventAccountDeleted = "account_deleted"

	// EventEmailVerificationSent is a verification message submitted.
	EventEmailVerificationSent = "email_verification_sent"

	// EventEmailVerified is an address proven, which is the state change
	// rather than the message.
	EventEmailVerified = "email_verified"

	// EventProfilePictureUpdated is an account's picture replaced.
	EventProfilePictureUpdated = "profile_picture_updated"

	// EventOneTimeAccessEmailSent is a one-time access code handed to the
	// queue for delivery. The code itself is never in the record: it exists
	// in the message and the hash, so the payload names the address only.
	EventOneTimeAccessEmailSent = "one_time_access_email_sent"

	// EventOneTimeAccessSignIn is a code exchanged for a session. It is a
	// sign-in of its own kind — no password was verified — so it is not the
	// sign_in event with a payload, which would make the log's one filter
	// unable to tell the two apart.
	EventOneTimeAccessSignIn = "one_time_access_sign_in"

	// EventProfilePictureReset is an account's picture cleared.
	EventProfilePictureReset = "profile_picture_reset"

	// EventGroupCreated is a user group that did not exist now does.
	EventGroupCreated = "group_created"

	// EventGroupUpdated is a user group's fields rewritten.
	EventGroupUpdated = "group_updated"

	// EventGroupDeleted is a user group removed.
	EventGroupDeleted = "group_deleted"

	// EventGroupMembersUpdated is a user group's member set replaced. The
	// membership change is the happening the reader looks for, so it is its
	// own event rather than the group_updated event with a payload — the
	// log's one filter cannot see inside a payload.
	EventGroupMembersUpdated = "group_members_updated"

	// EventUserGroupsUpdated is the account's own group set replaced — the
	// per-user direction of the membership change, which names the account
	// rather than the group and so is its own event too.
	EventUserGroupsUpdated = "user_groups_updated"

	// EventSignOut is a session ended by its own holder. It is the closing
	// counterpart of sign_in, and the record names the session it ended so
	// an operator can pair the two lines.
	EventSignOut = "sign_out"

	// EventSessionRevoked is a session ended by naming it — the account's
	// holder closing a device they no longer hold. It is not the sign_out
	// event with a payload: ending your own current session and ending one
	// you named are different happenings, and the log's one filter cannot
	// see inside a payload.
	EventSessionRevoked = "session_revoked"

	// EventAPIKeyCreated is a machine credential that did not exist now
	// does. The raw key is never in the record: it exists in the response
	// and the hash, so the payload names the key and its window only.
	EventAPIKeyCreated = "api_key_created"

	// EventAPIKeyRenewed is an expired key's secret and window replaced. It
	// is not the created event with a payload: a renewal answers "this
	// credential lived past its expiry", which is the fact a reader audits
	// for.
	EventAPIKeyRenewed = "api_key_renewed"

	// EventAPIKeyRevoked is a machine credential withdrawn. The stamp is
	// the happening; the row survives it.
	EventAPIKeyRevoked = "api_key_revoked"

	// EventAPIKeyExpiryEmailSent is the expiry reminder submitted. Like
	// every email record, it names the delivery, not the message's contents.
	EventAPIKeyExpiryEmailSent = "api_key_expiry_email_sent"

	// EventImpersonationStarted is a delegated session opened by an
	// administrator. The record's user_id names the TARGET account — the one
	// the requests will run as — and the payload's actor fields name the
	// administrator behind it (merged from the caller by the recorder), with
	// the reason the delegation exists.
	EventImpersonationStarted = "impersonation_started"

	// EventImpersonationStopped is a delegated session ended by the
	// administrator riding in it. The user_id names the target — matching
	// the started record so the pair reads together — and the payload's
	// actor fields name who stepped back out.
	EventImpersonationStopped = "impersonation_stopped"

	// EventMfaEnrollmentStarted is a second-factor ceremony that wrote an
	// unconfirmed authenticator. The secret is never in the record: the
	// enrollment's identifier is.
	EventMfaEnrollmentStarted = "mfa_enrollment_started"

	// EventMfaEnrollmentConfirmed is a second factor now active, proven by
	// a code the app rendered. The first confirmation also wrote the
	// account's recovery set, which the record does not repeat.
	EventMfaEnrollmentConfirmed = "mfa_enrollment_confirmed"

	// EventMfaEnrollmentFailed is a confirm ceremony whose code did not
	// verify. The refusal is the signal a reader of the log is watching
	// for — someone holds the QR code and cannot generate its codes.
	EventMfaEnrollmentFailed = "mfa_enrollment_failed"

	// EventMfaSignIn is a session the second factor completed. It is a
	// sign-in of its own kind, like the one-time access exchange: the
	// password was verified, but the session's opening was gated on a code.
	EventMfaSignIn = "mfa_sign_in"

	// EventMfaRecoveryRegenerated is a recovery set rewritten. The record
	// carries the count, never a value.
	EventMfaRecoveryRegenerated = "mfa_recovery_regenerated"

	// EventMfaDisabled is every second factor removed — by the account's
	// own proof, or by the last authenticator's removal.
	EventMfaDisabled = "mfa_disabled"

	// EventUserBanned is an account's access withdrawn for a stated term.
	// The record names the account it is about and carries the reason and
	// the expiry in the payload; the sessions the ban ended are counted
	// there, so a reader sees the blast radius without a second query.
	EventUserBanned = "user_banned"

	// EventUserUnbanned is a ban lifted. It is not the banned event with a
	// negative payload: applying a term and lifting it are different
	// happenings, and the log's one filter cannot see inside a payload.
	EventUserUnbanned = "user_unbanned"

	// EventPasswordResetEmailSent is a reset message submitted — by the
	// account's own request or by an administrator's trigger. The record
	// lands after the enqueue, the same convention the verification mail
	// keeps.
	EventPasswordResetEmailSent = "password_reset_email_sent"

	// EventPasswordReset is a credential replaced through a reset token.
	// The record commits in the transaction that swaps the hash and ends
	// the live sessions, so a reader sees the blast radius.
	EventPasswordReset = "password_reset"
)

// The trigger values the trigger_type column's enum allows. A record this
// application writes is always triggered by a caller or by the application
// itself; `external` is left for a record a third party causes, and nothing
// writes it today.
const (
	TriggerUser   = "user"
	TriggerSystem = "system"
)

// The action_status values the column's enum allows. A record is written when
// the action completed or when it was refused; `pending` and `unknown` are in
// the enum but nothing writes them, because a record is written once, after
// the outcome is known.
const (
	StatusSuccess = "success"
	StatusFailed  = "failed"
)
