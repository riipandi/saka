package user

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/riipandi/saka/framework/datastore"
)

// GroupSummary is the group fact an account view carries: enough to render
// the memberships beside the account, and the group names a deployment's
// clients match a group-based claim against. The id is the wire form
// (`ugrp_…`), so the view maps onto the contract without a second lookup.
type GroupSummary struct {
	ID          string
	Name        string
	DisplayName string
	UserCount   int32
	CreatedAt   time.Time
	UpdatedAt   *time.Time
}

// GroupDirectory is the seam the group feature answers: what one account
// belongs to, what many belong to, and which memberships a creation opens.
// It lives here because the account procedures answer groups inside their
// views, while the group tables belong to the group feature — an import the
// other way would cycle. The concrete reader is wired post-construction in
// the area's package, the same seam shape the MFA gate and the ban's side
// effects take.
//
// AttachGroups takes the wire forms the request carried, so the wire
// boundary stays where the request is parsed and the group feature keeps
// its own identifier conversion. An identifier that names no group fails
// the write.
type GroupDirectory interface {
	GroupsOfUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]GroupSummary, error)
	GroupsOfUsers(ctx context.Context, db datastore.Querier, userIDs []uuid.UUID) (map[uuid.UUID][]GroupSummary, error)
	AttachGroups(ctx context.Context, db datastore.Querier, userID uuid.UUID, groupIDs []string) error
}

// ErrGroupUnknown is a creation whose group ids name a group the deployment
// does not hold. The concrete directory raises it; the handler maps it the
// way it maps every other refusal.
var ErrGroupUnknown = errors.New("user: group does not exist")
