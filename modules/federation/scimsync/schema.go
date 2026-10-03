// Package scimsync provisions the deployment's accounts and groups to
// external applications that speak SCIM 2.0.
//
// Saka is the SCIM client here, not the server: one provider row per OIDC
// client names a remote base URL and the bearer token the sync presents,
// and one pass pushes the client's visible accounts and groups out until
// the remote matches the local snapshot. The visibility roll is the OIDC
// client's own: an unrestricted client provisions everyone, a restricted
// one provisions its allowed groups' members.
package scimsync

import (
	"time"

	"uuid"
)

// ProviderTable is the scim_service_providers table. Migration 00004 owns
// the schema; the feature ships against it.
const ProviderTable = "public.scim_service_providers"

// The identity tables the visibility roll reads. The constants live in the
// identity packages, but the adapters here take the columns directly — the
// roll is one query, not the user package's CRUD.
const (
	UserTable        = "public.users"
	GroupTable       = "public.user_groups"
	GroupMemberTable = "public.user_groups_users"
)

// ResourceProvider is the resource type an audit record names when the
// change is about a provider row.
const ResourceProvider = "scim_service_provider"

// Provider is one outbound provisioning target's row. The token column
// holds the sealed `enc:` form the schema's check constraint demands; the
// plaintext exists only in memory between an unseal and a request, and the
// Create answer carries the operator's one look at it.
type Provider struct {
	ID           uuid.UUID  `db:"id"`
	ClientID     string     `db:"oidc_client_id"`
	Endpoint     string     `db:"endpoint"`
	SealedToken  string     `db:"token"`
	CreatedAt    time.Time  `db:"created_at"`
	LastSyncedAt *time.Time `db:"last_synced_at"`
}
