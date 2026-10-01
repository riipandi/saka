package webhook

import (
	"slices"

	"github.com/riipandi/tango/internal/audit"
)

// The webhook event catalog: the dot-named events a receiver subscribes to,
// each mapped from the audit event that carries the same happening.
//
// The names are this surface's public contract — a receiver hardcodes them —
// so they are curated one by one rather than derived: the audit vocabulary's
// snake_case names say nothing about where the first dot belongs
// (`one_time_access_sign_in` would split as `one_time.access_sign_in`), and
// a mechanically derived name that reads badly is a name locked into the
// contract forever. The first segment names the domain, the rest the
// happening in the past tense, mirroring `user.created`.
//
// An audit event without a mapping here is never emitted: the catalog is
// also the emission filter, so a record can only be delivered under a name
// the catalog declared. A mapping added later is a new subscription choice,
// not a rename — the dot names never change once shipped.

// Event is one entry of the webhook event catalog.
type Event struct {
	// Name is the wire name: the body's `event` field, the
	// X-Webhook-Event header's value, and the subscription's entry.
	Name string
	// Source is the audit event the happening is recorded as. Empty for
	// the entries no audit record causes — the test event alone.
	Source string
	// Description is the one-sentence meaning the catalog serves.
	Description string
}

// catalog is the whole catalog, in the order a reader meets the domains:
// sessions first, then accounts, then what acts on them. The test event
// closes it, because it is the one entry no record causes.
var catalog = []Event{
	{Source: audit.EventSignIn, Name: "session.signed_in", Description: "A credential was verified and a session was opened."},
	{Source: audit.EventSignOut, Name: "session.signed_out", Description: "A session was ended by its own holder."},
	{Source: audit.EventSessionRevoked, Name: "session.revoked", Description: "A session was ended by naming it."},
	{Source: audit.EventOneTimeAccessSignIn, Name: "session.one_time_access_signed_in", Description: "A one-time access code was exchanged for a session."},
	{Source: audit.EventOneTimeAccessEmailSent, Name: "session.one_time_access_email_sent", Description: "A one-time access code was handed to the mail queue."},
	{Source: audit.EventMfaSignIn, Name: "session.mfa_signed_in", Description: "A session was completed by a second factor."},
	{Source: audit.EventImpersonationStarted, Name: "session.impersonation_started", Description: "A delegated session was opened by an administrator."},
	{Source: audit.EventImpersonationStopped, Name: "session.impersonation_stopped", Description: "A delegated session was ended by the administrator riding in it."},
	{Source: audit.EventWebauthnSignIn, Name: "session.passkey_signed_in", Description: "A session was opened by a passkey assertion with user verification."},
	{Source: audit.EventWebauthnReauthenticationGranted, Name: "session.reauthentication_granted", Description: "A step-up proof was answered by a passkey assertion."},
	{Source: audit.EventWebauthnReauthenticationConsumed, Name: "session.reauthentication_consumed", Description: "A step-up token was spent by a guarded procedure."},

	{Source: audit.EventAccountCreated, Name: "user.created", Description: "An account that did not exist now does."},
	{Source: audit.EventAccountUpdated, Name: "user.updated", Description: "An account's fields were rewritten."},
	{Source: audit.EventAccountDeleted, Name: "user.deleted", Description: "An account was removed."},
	{Source: audit.EventUserBanned, Name: "user.banned", Description: "An account's access was withdrawn for a stated term."},
	{Source: audit.EventUserUnbanned, Name: "user.unbanned", Description: "An account's ban was lifted."},
	{Source: audit.EventUserGroupsUpdated, Name: "user.groups_updated", Description: "An account's group memberships were replaced."},
	{Source: audit.EventUserRolesUpdated, Name: "user.roles_updated", Description: "An account's role set was replaced."},
	{Source: audit.EventUserPermissionsUpdated, Name: "user.permissions_updated", Description: "An account's direct grants were replaced."},
	{Source: audit.EventProfilePictureUpdated, Name: "user.picture_updated", Description: "An account's picture was replaced."},
	{Source: audit.EventProfilePictureReset, Name: "user.picture_reset", Description: "An account's picture was cleared."},
	{Source: audit.EventEmailVerificationSent, Name: "user.email_verification_sent", Description: "A verification message was submitted for delivery."},
	{Source: audit.EventEmailVerified, Name: "user.email_verified", Description: "An email address was proven to its account."},
	{Source: audit.EventEmailChangeRequested, Name: "user.email_change_requested", Description: "An address change was written and its token submitted."},
	{Source: audit.EventEmailChanged, Name: "user.email_changed", Description: "A pending address change was confirmed."},
	{Source: audit.EventPasswordResetEmailSent, Name: "user.password_reset_email_sent", Description: "A password reset message was submitted for delivery."},
	{Source: audit.EventPasswordReset, Name: "user.password_reset", Description: "A credential was replaced through a reset token."},
	{Source: audit.EventSigninBreachedPassword, Name: "user.signin_breached_password", Description: "A session opened on a credential the breach corpus knows — flagged for a forced change, not refused."},

	{Source: audit.EventGroupCreated, Name: "group.created", Description: "A user group that did not exist now does."},
	{Source: audit.EventGroupUpdated, Name: "group.updated", Description: "A user group's fields were rewritten."},
	{Source: audit.EventGroupDeleted, Name: "group.deleted", Description: "A user group was removed."},
	{Source: audit.EventGroupMembersUpdated, Name: "group.members_updated", Description: "A user group's member set was replaced."},
	{Source: audit.EventGroupAllowedClientsUpdated, Name: "group.allowed_clients_updated", Description: "A user group's client allowlist was replaced."},

	{Source: audit.EventWebauthnCredentialRegistered, Name: "passkey.enrolled", Description: "A passkey was enrolled onto an account."},
	{Source: audit.EventWebauthnCredentialRemoved, Name: "passkey.removed", Description: "A passkey was removed from an account."},
	{Source: audit.EventWebauthnCredentialRenamed, Name: "passkey.renamed", Description: "A passkey's display name was changed."},
	{Source: audit.EventWebauthnCredentialAdminRenamed, Name: "passkey.admin_renamed", Description: "A passkey's display name was changed by an administrator."},
	{Source: audit.EventWebauthnCredentialAdminRemoved, Name: "passkey.admin_removed", Description: "A passkey was removed by an administrator."},

	{Source: audit.EventAPIKeyCreated, Name: "api_key.created", Description: "A machine credential was created."},
	{Source: audit.EventAPIKeyRenewed, Name: "api_key.renewed", Description: "An expired key's secret and window were replaced."},
	{Source: audit.EventAPIKeyRevoked, Name: "api_key.revoked", Description: "A machine credential was revoked."},
	{Source: audit.EventAPIKeyExpiryEmailSent, Name: "api_key.expiry_email_sent", Description: "An API key expiry reminder was submitted for delivery."},

	{Source: audit.EventNotificationCreated, Name: "notification.created", Description: "A notification was published to its audience."},
	{Source: audit.EventNotificationCancelled, Name: "notification.cancelled", Description: "A notification was withdrawn."},

	{Source: audit.EventRoleCreated, Name: "role.created", Description: "A custom role was defined."},
	{Source: audit.EventRoleUpdated, Name: "role.updated", Description: "A role's name or description was replaced."},
	{Source: audit.EventRoleDeleted, Name: "role.deleted", Description: "A custom role was removed."},
	{Source: audit.EventRolePermissionsUpdated, Name: "role.permissions_updated", Description: "A role's permission set was replaced."},

	{Source: audit.EventMfaEnrollmentStarted, Name: "mfa.enrollment_started", Description: "A second-factor enrollment wrote an unconfirmed authenticator."},
	{Source: audit.EventMfaEnrollmentConfirmed, Name: "mfa.enrollment_confirmed", Description: "A second factor was confirmed active."},
	{Source: audit.EventMfaEnrollmentFailed, Name: "mfa.enrollment_failed", Description: "A second-factor confirmation code did not verify."},
	{Source: audit.EventMfaRecoveryRegenerated, Name: "mfa.recovery_regenerated", Description: "An account's recovery code set was rewritten."},
	{Source: audit.EventMfaRecoveryVerified, Name: "mfa.recovery_verified", Description: "A recovery code was spent as a standalone proof of identity."},
	{Source: audit.EventMfaDisabled, Name: "mfa.disabled", Description: "Every second factor was removed from an account."},

	{Source: audit.EventTestEmailSent, Name: "mailer.test_email_sent", Description: "The deployment's smoke message was submitted for delivery."},

	{Source: audit.EventSettingUpdated, Name: "setting.updated", Description: "A database-backed setting override was written."},
	{Source: audit.EventSettingReset, Name: "setting.reset", Description: "A database-backed setting returned to its catalog default."},

	{Source: audit.EventOidcClientCreated, Name: "oidc.client_created", Description: "An OIDC client was created."},
	{Source: audit.EventOidcClientUpdated, Name: "oidc.client_updated", Description: "An OIDC client's fields were rewritten."},
	{Source: audit.EventOidcClientDeleted, Name: "oidc.client_deleted", Description: "An OIDC client was removed."},
	{Source: audit.EventOidcClientGroupsUpdated, Name: "oidc.client_groups_updated", Description: "An OIDC client's group restriction was replaced."},
	{Source: audit.EventOidcClientSecretCreated, Name: "oidc.client_secret_created", Description: "A new client secret was minted."},
	{Source: audit.EventOidcClientSecretDeleted, Name: "oidc.client_secret_deleted", Description: "A client secret was withdrawn."},
	{Source: audit.EventOidcClientLogoUpdated, Name: "oidc.client_logo_updated", Description: "An OIDC client's logo was replaced."},
	{Source: audit.EventOidcClientLogoDeleted, Name: "oidc.client_logo_deleted", Description: "An OIDC client's logo was removed."},
	{Source: audit.EventOidcClientMetadataRefreshed, Name: "oidc.client_metadata_refreshed", Description: "A CIMD client's metadata was re-fetched and rewritten."},
	{Source: audit.EventOidcConsentRevoked, Name: "oidc.consent_revoked", Description: "An account's consent for a client was withdrawn."},
	{Source: audit.EventOidcSessionEnded, Name: "oidc.session_ended", Description: "An RP-initiated logout completed for one account and client."},
	{Source: audit.EventOidcDeviceAuthorized, Name: "oidc.device_authorized", Description: "A device flow user code was approved at the verification endpoint."},

	{Source: audit.EventDeviceLoginApproved, Name: "device_login.approved", Description: "A device login pairing request was approved."},
	{Source: audit.EventDeviceLoginDenied, Name: "device_login.denied", Description: "A device login pairing request was denied."},

	{Source: audit.EventCustomClaimCreated, Name: "custom_claim.created", Description: "A custom claim was created on an account or a group."},
	{Source: audit.EventCustomClaimUpdated, Name: "custom_claim.updated", Description: "A custom claim's key or value was rewritten."},
	{Source: audit.EventCustomClaimDeleted, Name: "custom_claim.deleted", Description: "A custom claim was removed."},

	{Source: audit.EventScimProviderCreated, Name: "scim.provider_created", Description: "An outbound SCIM provisioning target was attached to a client."},
	{Source: audit.EventScimProviderUpdated, Name: "scim.provider_updated", Description: "A SCIM target's endpoint or token was replaced."},
	{Source: audit.EventScimProviderDeleted, Name: "scim.provider_deleted", Description: "A SCIM provisioning target was removed."},
	{Source: audit.EventScimSyncCompleted, Name: "scim.sync_completed", Description: "A SCIM provisioning pass finished."},

	{Source: audit.EventJwksProvisioned, Name: "jwks.provisioned", Description: "A signing key pair was stored in the database."},
	{Source: audit.EventJwksInvalidated, Name: "jwks.invalidated", Description: "An AUTH_SECRET_KEY rotation retired stale signing rows and re-provisioned."},

	{Source: audit.EventWebhookCreated, Name: "webhook.endpoint_created", Description: "A webhook delivery endpoint was registered."},
	{Source: audit.EventWebhookUpdated, Name: "webhook.endpoint_updated", Description: "A webhook endpoint's fields were rewritten."},
	{Source: audit.EventWebhookDeleted, Name: "webhook.endpoint_deleted", Description: "A webhook endpoint was removed."},
	{Source: audit.EventWebhookSecretRotated, Name: "webhook.secret_rotated", Description: "A webhook endpoint's signing secret was replaced."},
	{Source: audit.EventWebhookTested, Name: "webhook.tested", Description: "A test delivery was queued to one webhook endpoint."},
	{Name: EventTest, Description: "A test delivery was sent, at the operator's command — no record caused it."},
}

// eventBySource maps the audit event a record carries onto the catalog
// entry it is delivered as. It is the emission's one lookup.
var eventBySource = func() map[string]Event {
	index := make(map[string]Event, len(catalog))
	for _, event := range catalog {
		if event.Source != "" {
			index[event.Source] = event
		}
	}
	return index
}()

// eventByName keys the catalog by wire name, the subscription check's one
// lookup.
var eventByName = func() map[string]Event {
	index := make(map[string]Event, len(catalog))
	for _, event := range catalog {
		index[event.Name] = event
	}
	return index
}()

// EventCatalog returns the catalog in its declared order. The caller owns
// the copy it answers, so a caller's sort cannot reorder the source.
func EventCatalog() []Event {
	return slices.Clone(catalog)
}

// EventForSource answers the catalog entry an audit record is delivered as.
// The second return is false for a source the catalog does not carry — the
// event that never becomes a delivery.
func EventForSource(source string) (Event, bool) {
	event, ok := eventBySource[source]
	return event, ok
}

// IsEvent reports whether a wire name is a catalog event. It is the
// boundary a subscription crosses: a name outside the catalog names nothing
// the deployment can ever deliver.
func IsEvent(name string) bool {
	_, ok := eventByName[name]
	return ok
}
