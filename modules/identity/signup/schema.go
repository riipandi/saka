package signup

import (
	"time"
	"uuid"
)

// SignupToken is one row of entity.TableSignupTokens, the view the feature reads and
// the operators manage. The raw token is not a field: only its hash is
// stored. GroupIDs are the groups its sign-ups join.
type SignupToken struct {
	ID         uuid.UUID
	UsageLimit int32
	UsageCount int32
	CreatedAt  time.Time
	ExpiresAt  time.Time
	GroupIDs   []uuid.UUID
}
