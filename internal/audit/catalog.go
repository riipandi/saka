package audit

import "slices"

// Event is one entry of the event catalog: a name a record can carry and the
// one-sentence description the API serves beside it. The descriptions are
// written for a reader choosing what to subscribe a webhook to; the comments
// on the constants above are written for a reader of the writer's code, and
// the two audiences read different sentences.
type Event struct {
	Name        string
	Description string
}

// catalog is the event catalog, in the order the constants above are
// declared. It is the single list of what a record can name: the webhook
// surface validates subscriptions and serves the catalog from it, so an
// event that is not here cannot be subscribed to, and a constant declared
// without an entry fails the catalog test.
var catalog = []Event{
	{EventSignIn, "A credential was verified and a session was opened."},
	{EventAccountCreated, "An account that did not exist now does."},
	{EventAccountUpdated, "An account's fields were rewritten."},
	{EventUsernameChanged, "An account's handle was replaced."},
	{EventAccountDeleted, "An account was removed."},
	{EventEmailVerificationSent, "A verification message was submitted for delivery."},
	{EventEmailVerified, "An email address was proven to its account."},
	{EventEmailChangeRequested, "An address change was written and its token submitted."},
	{EventEmailChanged, "A pending address change was confirmed."},
	{EventProfilePictureUpdated, "An account's picture was replaced."},
	{EventOneTimeAccessEmailSent, "A one-time access code was handed to the mail queue."},
	{EventOneTimeAccessSignIn, "A one-time access code was exchanged for a session."},
	{EventProfilePictureReset, "An account's picture was cleared."},
	{EventGroupCreated, "A user group that did not exist now does."},
	{EventGroupUpdated, "A user group's fields were rewritten."},
	{EventGroupDeleted, "A user group was removed."},
	{EventGroupMembersUpdated, "A user group's member set was replaced."},
	{EventUserGroupsUpdated, "An account's group memberships were replaced."},
	{EventSignOut, "A session was ended by its own holder."},
	{EventSessionRevoked, "A session was ended by naming it."},
	{EventWebauthnCredentialRegistered, "A passkey was enrolled onto an account."},
	{EventWebauthnCredentialRemoved, "A passkey was removed from an account."},
	{EventWebauthnCredentialRenamed, "A passkey's display name was changed."},
	{EventWebauthnCredentialAdminRenamed, "A passkey's display name was changed by an administrator."},
	{EventWebauthnCredentialAdminRemoved, "A passkey was removed by an administrator."},
	{EventWebauthnSignIn, "A session was opened by a passkey assertion with user verification."},
	{EventWebauthnReauthenticationGranted, "A step-up proof was answered by a passkey assertion, a password, or an email code."},
	{EventWebauthnReauthenticationCodeSent, "A reverification code was submitted for delivery."},
	{EventWebauthnReauthenticationConsumed, "A step-up token was spent by a guarded procedure."},
	{EventAPIKeyCreated, "A machine credential was created."},
	{EventAPIKeyRenewed, "An expired key's secret and window were replaced."},
	{EventAPIKeyRevoked, "A machine credential was revoked."},
	{EventAPIKeyExpiryEmailSent, "An API key expiry reminder was submitted for delivery."},
	{EventNotificationCreated, "A notification was published to its audience."},
	{EventNotificationCancelled, "A notification was withdrawn."},
	{EventRoleCreated, "A custom role was defined."},
	{EventRoleUpdated, "A role's name or description was replaced."},
	{EventRoleDeleted, "A custom role was removed."},
	{EventRolePermissionsUpdated, "A role's permission set was replaced."},
	{EventUserRolesUpdated, "An account's role set was replaced."},
	{EventUserPermissionsUpdated, "An account's direct grants were replaced."},
	{EventImpersonationStarted, "A delegated session was opened by an administrator."},
	{EventImpersonationStopped, "A delegated session was ended by the administrator riding in it."},
	{EventMfaEnrollmentStarted, "A second-factor enrollment wrote an unconfirmed authenticator."},
	{EventMfaEnrollmentConfirmed, "A second factor was confirmed active."},
	{EventMfaEnrollmentFailed, "A second-factor confirmation code did not verify."},
	{EventMfaSignIn, "A session was completed by a second factor."},
	{EventMfaRecoveryRegenerated, "An account's recovery code set was rewritten."},
	{EventMfaDisabled, "Every second factor was removed from an account."},
	{EventMfaRecoveryVerified, "A recovery code was spent as a standalone proof of identity."},
	{EventUserBanned, "An account's access was withdrawn for a stated term."},
	{EventUserUnbanned, "An account's ban was lifted."},
	{EventUserLocked, "An account was locked by the failed-attempt policy."},
	{EventUserUnlocked, "An account's lockout was lifted."},
	{EventPasswordRemoved, "An account's password credential was removed by its holder."},
	{EventPasswordResetEmailSent, "A password reset message was submitted for delivery."},
	{EventPasswordReset, "A credential was replaced through a reset token."},
	{EventPasswordAdded, "An account's first password credential was set by its holder."},
	{EventBlocklistEntryAdded, "A blocked identifier was stored in the sign-up blocklist."},
	{EventBlocklistEntryRemoved, "A blocked identifier was removed from the sign-up blocklist."},
	{EventSigninBreachedPassword, "A session opened on a credential the breach corpus knows — flagged for a forced change, not refused."},
	{EventTestEmailSent, "The deployment's smoke message was submitted for delivery."},
	{EventSettingUpdated, "A database-backed setting override was written."},
	{EventSettingReset, "A database-backed setting returned to its catalog default."},
	{EventOidcClientCreated, "An OIDC client was created."},
	{EventOidcClientUpdated, "An OIDC client's fields were rewritten."},
	{EventOidcClientDeleted, "An OIDC client was removed."},
	{EventOidcClientGroupsUpdated, "An OIDC client's group restriction was replaced."},
	{EventOidcClientSecretCreated, "A new client secret was minted."},
	{EventOidcClientSecretDeleted, "A client secret was withdrawn."},
	{EventOidcClientLogoUpdated, "An OIDC client's logo was replaced."},
	{EventOidcClientLogoDeleted, "An OIDC client's logo was removed."},
	{EventOidcClientMetadataRefreshed, "A CIMD client's metadata was re-fetched and rewritten."},
	{EventOidcConsentRevoked, "An account's consent for a client was withdrawn."},
	{EventOidcSessionEnded, "An RP-initiated logout completed for one account and client."},
	{EventOidcDeviceAuthorized, "A device flow user code was approved at the verification endpoint."},
	{EventDeviceLoginApproved, "A device login pairing request was approved."},
	{EventDeviceLoginDenied, "A device login pairing request was denied."},
	{EventGroupAllowedClientsUpdated, "A user group's client allowlist was replaced."},
	{EventCustomClaimCreated, "A custom claim was created on an account or a group."},
	{EventCustomClaimUpdated, "A custom claim's key or value was rewritten."},
	{EventCustomClaimDeleted, "A custom claim was removed."},
	{EventScimProviderCreated, "An outbound SCIM provisioning target was attached to a client."},
	{EventScimProviderUpdated, "A SCIM target's endpoint or token was replaced."},
	{EventScimProviderDeleted, "A SCIM provisioning target was removed."},
	{EventScimSyncCompleted, "A SCIM provisioning pass finished."},
	{EventJwksProvisioned, "A signing key pair was stored in the database."},
	{EventJwksInvalidated, "An AUTH_SECRET_KEY rotation retired stale signing rows and re-provisioned."},
	{EventWebhookCreated, "A webhook delivery endpoint was registered."},
	{EventWebhookUpdated, "A webhook endpoint's fields were rewritten."},
	{EventWebhookDeleted, "A webhook endpoint was removed."},
	{EventWebhookSecretRotated, "A webhook endpoint's signing secret was replaced."},
	{EventWebhookTested, "A test delivery was queued to one webhook endpoint."},
	{EventQueueTaskCancelled, "A pending task was removed from the queue before a worker claimed it."},
	{EventQueueDeadReplayed, "Dead tasks were re-enqueued from the archive, for one queue or every queue."},
	{EventQueuePendingFlushed, "Every unclaimed task was removed from the queue."},
	{EventQueueCompletedFlushed, "Every archived task record was removed."},
	{EventSchedulerJobRunNow, "A scheduled job's task was enqueued immediately, without advancing its schedule."},
	{EventOauthSsoConnectionCreated, "An OAuth provider connection was registered."},
	{EventOauthSsoConnectionUpdated, "An OAuth provider connection's fields were rewritten."},
	{EventOauthSsoConnectionDeleted, "An OAuth provider connection was removed."},
	{EventOauthSsoSignIn, "A session opened through a linked OAuth provider identity."},
	{EventOauthSsoAccountCreated, "An account was provisioned by an OAuth sign-in."},
	{EventOauthSsoAccountLinked, "An OAuth provider identity was bound into an account."},
	{EventOauthSsoAccountUnlinked, "An OAuth provider identity was removed from an account."},
}

// eventIndex is the catalog keyed by name, built once from the slice so a
// lookup does not walk the catalog.
var eventIndex = func() map[string]struct{} {
	index := make(map[string]struct{}, len(catalog))
	for _, event := range catalog {
		index[event.Name] = struct{}{}
	}
	return index
}()

// Catalog returns the event catalog in declaration order. The caller owns
// the copy it answers, so a caller's sort cannot reorder the source.
func Catalog() []Event {
	return slices.Clone(catalog)
}

// IsEvent reports whether a name is a catalog event. It is the boundary a
// subscription crosses: a name outside the catalog names nothing a record
// can ever carry.
func IsEvent(name string) bool {
	_, ok := eventIndex[name]
	return ok
}
