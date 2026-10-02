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
const (
	// EventSignIn is a credential verified and a session opened.
	EventSignIn = "sign_in"

	// EventAccountCreated is an account that did not exist now does. Sign-up
	// and the administrator's own creation both write it.
	EventAccountCreated = "account_created"

	// EventAccountUpdated is an account's fields rewritten.
	EventAccountUpdated = "account_updated"

	// EventUsernameChanged is an account's handle replaced — its own
	// happening beside the profile update that carried it, because the
	// readers the unique index serves want the rename named. The payload
	// keeps the old handle; the row carries the new one.
	EventUsernameChanged = "username_changed"

	// EventAccountDeleted is an account removed.
	EventAccountDeleted = "account_deleted"

	// EventEmailVerificationSent is a verification message submitted.
	EventEmailVerificationSent = "email_verification_sent"

	// EventEmailVerified is an address proven, which is the state change
	// rather than the message.
	EventEmailVerified = "email_verified"

	// EventEmailChangeRequested is a pending change written and its token
	// submitted to the address the change moves to.
	EventEmailChangeRequested = "email_change_requested"

	// EventEmailChanged is a pending change confirmed: the account's address
	// moved and the token was consumed.
	EventEmailChanged = "email_changed"

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

	// EventWebauthnCredentialRegistered is a passkey enrolled onto an
	// account. The record names the credential and the authenticator model;
	// no key material is ever in the payload.
	EventWebauthnCredentialRegistered = "webauthn_credential_registered"

	// EventWebauthnCredentialRemoved is a passkey deleted — by its holder,
	// through the surface that proves the session.
	EventWebauthnCredentialRemoved = "webauthn_credential_removed"

	// EventWebauthnCredentialRenamed is a passkey's display name changed.
	EventWebauthnCredentialRenamed = "webauthn_credential_renamed"

	// EventWebauthnCredentialAdminRenamed is a passkey's display name
	// changed by an administrator over a named account.
	EventWebauthnCredentialAdminRenamed = "webauthn_credential_admin_renamed"

	// EventWebauthnCredentialAdminRemoved is a passkey removed by an
	// administrator over a named account.
	EventWebauthnCredentialAdminRemoved = "webauthn_credential_admin_removed"

	// EventWebauthnSignIn is a session opened by a passkey assertion with
	// user verification — the whole proof in one step, no second factor
	// owed.
	EventWebauthnSignIn = "webauthn_sign_in"

	// EventWebauthnReauthenticationGranted is a step-up proof answered by a
	// passkey assertion, a password, or an email code; the record names the
	// factor and the token's window, never its value.
	EventWebauthnReauthenticationGranted = "webauthn_reauthentication_granted"

	// EventWebauthnReauthenticationCodeSent is a reverification code handed
	// to the email queue. The record names the send; the code itself is
	// never in the log.
	EventWebauthnReauthenticationCodeSent = "webauthn_reauthentication_code_sent"

	// EventWebauthnReauthenticationConsumed is a step-up token spent by a
	// guarded procedure. One token is one proof: the record is how a
	// double-spend attempt shows up in the log.
	EventWebauthnReauthenticationConsumed = "webauthn_reauthentication_consumed"

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

	// EventNotificationCreated is a notification published to its audience.
	// The payload names the category, the audience kind, and whether an
	// email pass was asked for.
	EventNotificationCreated = "notification_created"

	// EventNotificationCancelled is a notification withdrawn. The stamp is
	// the happening; the row survives it, and the record names it.
	EventNotificationCancelled = "notification_cancelled"

	// EventRoleCreated is a custom role defined. The payload names the slug
	// the claims will carry.
	EventRoleCreated = "role_created"

	// EventRoleUpdated is a role's name or description replaced.
	EventRoleUpdated = "role_updated"

	// EventRoleDeleted is a custom role removed. The roles accounts still
	// hold are refused, so the deletion strips nothing silently.
	EventRoleDeleted = "role_deleted"

	// EventRolePermissionsUpdated is a role's permission set replaced. The
	// payload counts the slugs the set now carries.
	EventRolePermissionsUpdated = "role_permissions_updated"

	// EventUserRolesUpdated is an account's role set replaced. The record's
	// user_id names the account; the payload counts the roles it now holds.
	EventUserRolesUpdated = "user_roles_updated"

	// EventUserPermissionsUpdated is an account's direct grants replaced.
	// The record's user_id names the account; the payload counts the slugs
	// it now carries.
	EventUserPermissionsUpdated = "user_permissions_updated"

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

	// EventMfaRecoveryVerified is a recovery code spent as a standalone
	// proof of identity — the step-up verification, not a sign-in. The
	// consumption is the happening: the set's remaining count dropped.
	EventMfaRecoveryVerified = "mfa_recovery_verified"

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

	// EventPasswordAdded is an account's first credential set through the
	// self-service flow — the proof rode the step-up header, and the
	// record commits in the transaction that inserts the hash.
	EventPasswordAdded = "password_added"

	// EventSigninBreachedPassword is a session that opened on a credential
	// the breach corpus knows — the flag-and-force-change answer, not a
	// refusal: a rejection here is an oracle and locks a breached account
	// out of the recovery it needs. The record lands after the session's
	// own, carrying no credential material.
	EventSigninBreachedPassword = "signin_breached_password"

	// EventTestEmailSent is the deployment's own smoke message submitted —
	// the administrator's proof that the mailer is configured and
	// reachable. The payload names the address it went to, which the
	// request may have redirected away from the caller's own.
	EventTestEmailSent = "test_email_sent"

	// EventSettingUpdated is a database-backed setting written — an override
	// created or replaced — through the settings surface. The payload names
	// the key and the flags, never the value: a value may be a secret, and
	// the log is not the place a secret is kept a second time.
	EventSettingUpdated = "setting_updated"

	// EventSettingReset is a database-backed setting returned to its catalog
	// default — the override removed. The record commits in the transaction
	// that drops the row.
	EventSettingReset = "setting_reset"

	// EventOidcClientCreated is an OIDC client that did not exist now does.
	// The payload names the client id and the defining account; the first
	// secret is never in the record — it exists in the response and the
	// hash.
	EventOidcClientCreated = "oidc_client_created"

	// EventOidcClientUpdated is a client's fields rewritten. The secrets,
	// the logo, and the group restriction are not this event's happenings:
	// each has its own.
	EventOidcClientUpdated = "oidc_client_updated"

	// EventOidcClientDeleted is a client removed. The client id rides the
	// payload — it is the operator's word, not a row UUID, so the uuid
	// resource_id column cannot carry it — and it is the one fact a later
	// reader cannot reconstruct.
	EventOidcClientDeleted = "oidc_client_deleted"

	// EventOidcClientGroupsUpdated is a client's group restriction
	// replaced. It is its own event, because the log's one filter cannot
	// see inside a payload.
	EventOidcClientGroupsUpdated = "oidc_client_groups_updated"

	// EventOidcClientSecretCreated is one more secret minted for a client.
	// The raw value is never in the record; the payload names the secret's
	// identifier.
	EventOidcClientSecretCreated = "oidc_client_secret_created"

	// EventOidcClientSecretDeleted is one secret withdrawn. The payload
	// names which one, so a reader can pair it with the creation.
	EventOidcClientSecretDeleted = "oidc_client_secret_deleted"

	// EventOidcClientLogoUpdated is a client's logo replaced. The payload
	// names the kind the bytes proved, not the bytes.
	EventOidcClientLogoUpdated = "oidc_client_logo_updated"

	// EventOidcClientLogoDeleted is a client's logo removed.
	EventOidcClientLogoDeleted = "oidc_client_logo_deleted"

	// EventOidcClientMetadataRefreshed is a CIMD document re-fetched on
	// the operator's force — the client's redirects and grants rewritten
	// from what the document now declares.
	EventOidcClientMetadataRefreshed = "oidc_client_metadata_refreshed"

	// EventOidcConsentRevoked is an account's consent for a client
	// withdrawn. The withdrawal kills the grants and tokens the consent
	// issued, so the payload names the account and the client.
	EventOidcConsentRevoked = "oidc_consent_revoked"

	// EventOidcSessionEnded is an RP-initiated logout completed for one
	// account and client. The grants and tokens die with it; whether the
	// authorized-client ledger died too is the deployment's configured
	// answer, so a reader pairs this event with the configuration.
	EventOidcSessionEnded = "oidc_session_ended"

	// EventOidcDeviceAuthorized is a device flow user code approved at
	// the verification endpoint. The payload names the client and the
	// account; the device code itself never rides the record.
	EventOidcDeviceAuthorized = "oidc_device_authorized"

	// EventDeviceLoginApproved is a device login pairing request the
	// approving device accepted — the browser on the other side will
	// open its session at the next exchange. The payload names the
	// request row.
	EventDeviceLoginApproved = "device_login_approved"

	// EventDeviceLoginDenied is a pairing request refused. The payload
	// names the request row.
	EventDeviceLoginDenied = "device_login_denied"

	// EventGroupAllowedClientsUpdated is the group's client allowlist
	// replaced — the group-side direction of the client restriction, the
	// mirror of the members' own event. The payload counts the roll.
	EventGroupAllowedClientsUpdated = "group_allowed_clients_updated"

	// EventCustomClaimCreated is a claim that did not exist now does, on an
	// account or a group — the payload's subject names which.
	EventCustomClaimCreated = "custom_claim_created"

	// EventCustomClaimUpdated is a claim's key or value rewritten.
	EventCustomClaimUpdated = "custom_claim_updated"

	// EventCustomClaimDeleted is a claim removed.
	EventCustomClaimDeleted = "custom_claim_deleted"

	// EventScimProviderCreated is an outbound SCIM provisioning target
	// attached to a client. The payload names the client; the token
	// never rides the record.
	EventScimProviderCreated = "scim_provider_created"

	// EventScimProviderUpdated is a target's endpoint or token replaced.
	EventScimProviderUpdated = "scim_provider_updated"

	// EventScimProviderDeleted is a target removed. The remote data the
	// sync pushed stays where it is.
	EventScimProviderDeleted = "scim_provider_deleted"

	// EventScimSyncCompleted is one provisioning pass that finished. The
	// payload counts what the pass created, updated, and deleted on the
	// remote; the remote's own data never rides the record.
	EventScimSyncCompleted = "scim_sync_completed"

	// EventJwksProvisioned is a signing key pair stored in the database's
	// jwks table. The payload names the kid and the algorithm; the private
	// material is sealed at rest and never rides the record.
	EventJwksProvisioned = "jwks_provisioned"

	// EventJwksInvalidated is the auto-invalidation an AUTH_SECRET_KEY
	// rotation runs: the rows sealed by the previous secret are retired
	// and a replacement pair is provisioned. The payload names the
	// replacement's kid and algorithm plus how many rows were retired.
	EventJwksInvalidated = "jwks_invalidated"

	// EventWebhookCreated is a delivery endpoint registered. The signing
	// secret is never in the record — it exists in the response and the
	// sealed column.
	EventWebhookCreated = "webhook_created"

	// EventWebhookUpdated is an endpoint's fields rewritten.
	EventWebhookUpdated = "webhook_updated"

	// EventWebhookDeleted is an endpoint removed. Its deliveries survive
	// with the endpoint's identifier nulled, so the history outlives the
	// destination.
	EventWebhookDeleted = "webhook_deleted"

	// EventWebhookSecretRotated is an endpoint's signing secret replaced.
	// The plaintext is never in the record: it exists in the response and
	// the sealed column.
	EventWebhookSecretRotated = "webhook_secret_rotated"

	// EventWebhookTested is a test delivery queued to one endpoint — the
	// queueing is the happening; the delivery rows carry the outcome.
	EventWebhookTested = "webhook_tested"

	// EventQueueTaskCancelled is a pending task an administrator removed
	// before a worker claimed it. The record names the task's wire identity;
	// a claimed task cannot be cancelled and writes nothing.
	EventQueueTaskCancelled = "queue_task_cancelled"

	// EventQueueDeadReplayed is the archive's dead tasks re-enqueued — one
	// queue's, or every queue's. The payload counts what went back and names
	// the queue; an absent name covered them all.
	EventQueueDeadReplayed = "queue_dead_replayed"

	// EventQueuePendingFlushed is every unclaimed task removed. The payload
	// counts what went; a claimed task is in flight and survives a flush.
	EventQueuePendingFlushed = "queue_pending_flushed"

	// EventQueueCompletedFlushed is every archived record removed, retention
	// notwithstanding. The payload counts what went.
	EventQueueCompletedFlushed = "queue_completed_flushed"

	// EventSchedulerJobRunNow is a job's task enqueued on demand, without
	// advancing the job's schedule. The record names the job's wire identity.
	EventSchedulerJobRunNow = "scheduler_job_run_now"
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
