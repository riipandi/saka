package signup

import (
	"time"
	"uuid"
)

// SignupTokenTable is the table holding the signup tokens an operator issues.
// The migrations own the schema; this constant is how Go code names it, so a
// table rename touches one line.
const SignupTokenTable = "public.signup_tokens"

// SignupTokenGroupTable is the junction naming the groups every account a
// token creates joins. The rows are the token's to cascade away with it.
const SignupTokenGroupTable = "public.signup_tokens_user_groups"

// SignupToken is one row of SignupTokenTable, the view the feature reads and
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
