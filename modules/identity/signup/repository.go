package signup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"
	"uuid"

	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/modules/identity/password"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/modules/identity/usergroup"
)

// Repository writes the account a sign-up creates and consumes the token it
// was created under. Every method takes the query surface, so the service
// passes either the pool or the transaction it runs in.
type Repository struct{}

// NewRepository builds the repository over the shared pool.
func NewRepository() *Repository {
	return &Repository{}
}

// FindSignupTokenByHash reads the token a raw value hashes to, with the
// groups its sign-ups join. The raw value is never stored: only the caller's
// hash reaches this query.
func (r *Repository) FindSignupTokenByHash(ctx context.Context, db datastore.Querier, tokenHash string) (*SignupToken, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "usage_limit", "usage_count", "created_at", "expires_at")
	sb.From(SignupTokenTable)
	sb.Where(sb.Equal("token_hash", tokenHash))

	query, args := sb.Build()
	var row SignupToken
	err := db.QueryRow(ctx, query, args...).Scan(&row.ID, &row.UsageLimit, &row.UsageCount, &row.CreatedAt, &row.ExpiresAt)
	if errors.Is(err, datastore.ErrNoRows) {
		return nil, datastore.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("signup: find token: %w", err)
	}

	groups, err := r.TokenGroups(ctx, db, row.ID)
	if err != nil {
		return nil, err
	}
	row.GroupIDs = groups
	return &row, nil
}

// TokenGroups reads the groups one issued token puts its sign-ups into,
// ordered by the group's name so the answer does not depend on insert order.
func (r *Repository) TokenGroups(ctx context.Context, db datastore.Querier, tokenID uuid.UUID) ([]uuid.UUID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("j.user_group_id")
	sb.From(SignupTokenGroupTable + " j")
	sb.JoinWithOption(sqlbuilder.LeftJoin, usergroup.GroupTable+" g", "g.id = j.user_group_id")
	sb.Where(sb.Equal("j.signup_token_id", tokenID))
	sb.OrderBy("lower(g.name)", "g.id")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("signup: token groups: %w", err)
	}
	defer rows.Close()

	groups := []uuid.UUID{}
	for rows.Next() {
		var groupID uuid.UUID
		if err := rows.Scan(&groupID); err != nil {
			return nil, fmt.Errorf("signup: token groups: %w", err)
		}
		groups = append(groups, groupID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("signup: token groups: %w", err)
	}
	return groups, nil
}

// CreateTokenGroups writes the token's group links. The caller has already
// checked the groups exist; the junction's foreign keys are the last line of
// defense.
func (r *Repository) CreateTokenGroups(ctx context.Context, db datastore.Querier, tokenID uuid.UUID, groupIDs []uuid.UUID) error {
	for _, groupID := range groupIDs {
		ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
		ib.InsertInto(SignupTokenGroupTable)
		ib.Cols("signup_token_id", "user_group_id")
		ib.Values(tokenID, groupID)

		query, args := ib.Build()
		if _, err := db.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("signup: token groups: %w", err)
		}
	}
	return nil
}

// AddUserToGroups makes the account a member of the groups its signup token
// carried. A group deleted between the token's issue and its use simply
// joins fewer groups: the junction's cascade is the deletion's business, and
// the sign-up must not fail over a vanished group.
func (r *Repository) AddUserToGroups(ctx context.Context, db datastore.Querier, userID uuid.UUID, groupIDs []uuid.UUID) error {
	for _, groupID := range groupIDs {
		ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
		ib.InsertInto(usergroup.GroupMemberTable)
		ib.Cols("user_id", "user_group_id")
		ib.Values(userID, groupID)

		query, args := ib.Build()
		if _, err := db.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("signup: join group: %w", err)
		}
	}
	return nil
}

// CreateUser inserts the account row and returns its identifier.
func (r *Repository) CreateUser(ctx context.Context, db datastore.Querier, row user.UserSchema) (uuid.UUID, error) {
	row.ID = uuid.NewV7()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(user.UserTable)
	ib.Cols("id", "username", "email", "first_name", "last_name", "display_name")
	// The name columns are nullable and an absent name is NULL, not the
	// empty string the struct's zero value carries.
	ib.Values(row.ID, row.Username, row.Email, nullIfEmpty(row.FirstName), nullIfEmpty(row.LastName), row.DisplayName)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return uuid.Nil(), err
	}
	return row.ID, nil
}

// nullIfEmpty turns an absent optional field into the SQL NULL its column
// stores.
func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// CreatePassword stores the account's primary credential.
func (r *Repository) CreatePassword(ctx context.Context, db datastore.Querier, row password.UserPasswordSchema) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(password.UserPasswordTable)
	ib.Cols("user_id", "password_hash")
	ib.Values(row.UserID, row.PasswordHash)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return err
	}
	return nil
}

// ConsumeSignupToken counts one use against the token. The conditional update
// is the whole single-use rule: a token that is spent or expired between the
// read and this statement bumps no row, and the caller refuses the signup.
func (r *Repository) ConsumeSignupToken(ctx context.Context, db datastore.Querier, tokenID uuid.UUID, at time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(SignupTokenTable)
	ub.Set(ub.Assign("usage_count", sqlbuilder.Raw("usage_count + 1")))
	ub.Where(
		ub.Equal("id", tokenID),
		ub.LessThan("usage_count", sqlbuilder.Raw("usage_limit")),
		ub.GreaterThan("expires_at", at),
	)

	query, args := ub.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("signup: consume token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidToken
	}
	return nil
}

// errUniqueViolation reports whether the insert failed on a unique index, the
// way the username and email indexes answer a duplicate account.
func errUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// AssertGroupsExist refuses a group id that names no group, so a token never
// carries a link its sign-ups cannot resolve.
func (r *Repository) AssertGroupsExist(ctx context.Context, db datastore.Querier, groupIDs []uuid.UUID) error {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(usergroup.GroupTable)
	sb.Where(sb.In("id", groupIDArgs(groupIDs)...))

	query, args := sb.Build()
	var found int
	if err := db.QueryRow(ctx, query, args...).Scan(&found); err != nil {
		return fmt.Errorf("signup: check groups: %w", err)
	}
	if found != len(groupIDs) {
		return ErrGroupNotFound
	}
	return nil
}

// CreateSignupToken inserts the hashed row, its group links, and answers the
// identifier. The raw value is the caller's to show once; only the hash
// reaches this table.
func (r *Repository) CreateSignupToken(ctx context.Context, db datastore.Querier, tokenHash string, usageLimit int32, expiresAt time.Time, groupIDs []uuid.UUID) (uuid.UUID, error) {
	id := uuid.NewV7()

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(SignupTokenTable)
	ib.Cols("id", "token_hash", "usage_limit", "expires_at")
	ib.Values(id, tokenHash, usageLimit, expiresAt)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return uuid.Nil(), fmt.Errorf("signup: create token: %w", err)
	}
	if err := r.CreateTokenGroups(ctx, db, id, groupIDs); err != nil {
		return uuid.Nil(), err
	}
	return id, nil
}

// tokenSortColumns is the whitelist a list's sort key resolves through. The
// names are the wire values `ListSignupTokensRequest.sort_by` validates
// against.
var tokenSortColumns = map[string]string{
	"created_at":  "created_at",
	"expires_at":  "expires_at",
	"usage_count": "usage_count",
	"usage_limit": "usage_limit",
}

// ListSignupTokens answers one page of the issued tokens, ordered as the
// caller asked (absent a choice, newest first), with the total count the
// pagination metadata needs.
func (r *Repository) ListSignupTokens(ctx context.Context, db datastore.Querier, sortBy string, ascending bool, offset, limit int) ([]SignupToken, int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "usage_limit", "usage_count", "created_at", "expires_at")
	sb.From(SignupTokenTable)
	sb.OrderBy(datastore.ListOrder(tokenSortColumns, sortBy, "created_at", ascending), "id")
	sb.Limit(limit).Offset(offset)

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("signup: list tokens: %w", err)
	}
	defer rows.Close()

	tokens := []SignupToken{}
	for rows.Next() {
		var row SignupToken
		if scanErr := rows.Scan(&row.ID, &row.UsageLimit, &row.UsageCount, &row.CreatedAt, &row.ExpiresAt); scanErr != nil {
			return nil, 0, fmt.Errorf("signup: list tokens: %w", scanErr)
		}
		tokens = append(tokens, row)
	}
	// The outer `err` is the one attachGroups reuses below, so the
	// iteration's close check assigns it rather than shadowing it.
	err = rows.Err()
	if err != nil {
		return nil, 0, fmt.Errorf("signup: list tokens: %w", err)
	}

	// The junction rows ride along in one query: a page holds at most a
	// hundred tokens, and one grouped read beats one read per token.
	tokens, err = r.attachGroups(ctx, db, tokens)
	if err != nil {
		return nil, 0, err
	}

	cb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	cb.Select("count(*)")
	cb.From(SignupTokenTable)
	query, args = cb.Build()
	var total int
	if err := db.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("signup: count tokens: %w", err)
	}
	return tokens, total, nil
}

// attachGroups fills every token's group list. The ordering by name makes
// the answer independent of insert order, the way a single token's read is.
func (r *Repository) attachGroups(ctx context.Context, db datastore.Querier, tokens []SignupToken) ([]SignupToken, error) {
	if len(tokens) == 0 {
		return tokens, nil
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("j.signup_token_id", "j.user_group_id")
	sb.From(SignupTokenGroupTable + " j")
	sb.JoinWithOption(sqlbuilder.LeftJoin, usergroup.GroupTable+" g", "g.id = j.user_group_id")
	sb.Where(sb.In("j.signup_token_id", tokenIDs(tokens)...))
	sb.OrderBy("lower(g.name)", "g.id")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("signup: token groups: %w", err)
	}
	defer rows.Close()

	index := make(map[uuid.UUID]int, len(tokens))
	for i, token := range tokens {
		index[token.ID] = i
	}
	for rows.Next() {
		var tokenID, groupID uuid.UUID
		if err := rows.Scan(&tokenID, &groupID); err != nil {
			return nil, fmt.Errorf("signup: token groups: %w", err)
		}
		if i, ok := index[tokenID]; ok {
			tokens[i].GroupIDs = append(tokens[i].GroupIDs, groupID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("signup: token groups: %w", err)
	}
	return tokens, nil
}

// tokenIDs answers the identifiers attachGroups filters by.
func tokenIDs(tokens []SignupToken) []any {
	ids := make([]any, len(tokens))
	for i, token := range tokens {
		ids[i] = token.ID
	}
	return ids
}

// groupIDArgs answers the identifiers the existence check filters by. The
// values ride as `any` because `Flatten` would unpack a uuid — an array of
// bytes — into its bytes.
func groupIDArgs(groupIDs []uuid.UUID) []any {
	ids := make([]any, len(groupIDs))
	for i, groupID := range groupIDs {
		ids[i] = groupID
	}
	return ids
}

// DeleteSignupToken removes an issued token. It answers whether a row was
// removed, so the caller refuses an id that names nothing.
func (r *Repository) DeleteSignupToken(ctx context.Context, db datastore.Querier, id uuid.UUID) (bool, error) {
	db2 := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	db2.DeleteFrom(SignupTokenTable)
	db2.Where(db2.Equal("id", id))

	query, args := db2.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("signup: delete token: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
