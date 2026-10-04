package oauthsso

import (
	"context"
	"errors"
	"fmt"
	"time"

	"encoding/json/v2"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/riipandi/saka/database/entity"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/identity/user"
)

// The failures the repository reports. The service maps them onto the
// errors the handler turns into connect codes.
var (
	// ErrConnectionNotFound is an identifier or slug that names no live
	// connection.
	ErrConnectionNotFound = errors.New("oauthsso: connection not found")

	// ErrProviderTaken is a create or update whose provider slug the
	// unique index already holds — one connection per provider.
	ErrProviderTaken = errors.New("oauthsso: a connection with this provider slug already exists")
)

const connectionColumns = `id, kind, provider, display_name, discovery_url, endpoints,
	client_id, client_secret, scopes, attribute_mapping, enabled, created_at, updated_at`

// Repository reads and writes the connection rows. The linked accounts
// and the flows get their queries beside these as their phases land; the
// connection CRUD is the phase 2 surface.
type Repository struct {
	pool *datastore.Postgres
}

// NewRepository builds the repository over the shared pool.
func NewRepository(pool *datastore.Postgres) *Repository {
	return &Repository{pool: pool}
}

// Insert writes one connection row. The caller has already sealed the
// secret and validated the endpoints; the row's id comes from the
// database's uuidv7 default.
func (r *Repository) Insert(ctx context.Context, db datastore.Querier, conn Connection) (uuid.UUID, time.Time, error) {
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(entity.TableOAuthConnections)
	sb.Cols("kind", "provider", "display_name", "discovery_url", "endpoints",
		"client_id", "client_secret", "scopes", "attribute_mapping", "enabled")
	sb.Values(
		string(conn.Kind), conn.Provider, conn.DisplayName, nullIfEmpty(conn.DiscoveryURL),
		endpointsJSON(conn.Endpoints), conn.ClientID, conn.ClientSecret,
		scopesJSON(conn.Scopes), mappingJSON(conn.AttributeMapping, conn.CustomAttributes), conn.Enabled,
	)
	sb.SQL("RETURNING id, created_at")
	query, args := sb.Build()

	var (
		id     uuid.UUID
		create time.Time
	)
	err := db.QueryRow(ctx, query, args...).Scan(&id, &create)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return uuid.Nil(), time.Time{}, ErrProviderTaken
		}
		return uuid.Nil(), time.Time{}, err
	}
	return id, create, nil
}

// List answers every live connection in provider order — the order the
// operator's screen and the slug-named resolution both read.
func (r *Repository) List(ctx context.Context) ([]Connection, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(connectionColumns)
	sb.From(entity.TableOAuthConnections)
	sb.OrderBy("provider")
	query, args := sb.Build()

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Connection
	for rows.Next() {
		conn, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, conn)
	}
	return out, rows.Err()
}

// ByID reads one connection by its row id, on the query surface it is
// handed, so a caller's transaction reads its own view.
func (r *Repository) ByID(ctx context.Context, db datastore.Querier, id uuid.UUID) (Connection, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(connectionColumns)
	sb.From(entity.TableOAuthConnections)
	sb.Where(sb.Equal("id", id))
	query, args := sb.Build()
	return scanConnection(db.QueryRow(ctx, query, args...))
}

// ByProvider reads one connection by its provider slug — the word
// BeginSignIn names.
func (r *Repository) ByProvider(ctx context.Context, db datastore.Querier, provider string) (Connection, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(connectionColumns)
	sb.From(entity.TableOAuthConnections)
	sb.Where(sb.Equal("provider", provider))
	query, args := sb.Build()
	return scanConnection(db.QueryRow(ctx, query, args...))
}

// Update rewrites one connection's editable columns and answers the row's
// new updated_at. The caller has already sealed whatever secret it is
// replacing and validated whatever endpoints it is storing; the WHERE is
// the id alone, because the connection is not single-use state.
func (r *Repository) Update(ctx context.Context, db datastore.Querier, id uuid.UUID, conn Connection) (time.Time, error) {
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(entity.TableOAuthConnections)
	sb.Set(
		sb.Assign("display_name", conn.DisplayName),
		sb.Assign("discovery_url", nullIfEmpty(conn.DiscoveryURL)),
		sb.Assign("endpoints", endpointsJSON(conn.Endpoints)),
		sb.Assign("client_id", conn.ClientID),
		sb.Assign("client_secret", conn.ClientSecret),
		sb.Assign("scopes", scopesJSON(conn.Scopes)),
		sb.Assign("attribute_mapping", mappingJSON(conn.AttributeMapping, conn.CustomAttributes)),
		sb.Assign("enabled", conn.Enabled),
	)
	sb.Where(sb.Equal("id", id))
	sb.SQL("RETURNING updated_at")
	query, args := sb.Build()

	var updated *time.Time
	err := db.QueryRow(ctx, query, args...).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrConnectionNotFound
	}
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return time.Time{}, ErrProviderTaken
		}
		return time.Time{}, err
	}
	if updated == nil {
		return time.Time{}, nil
	}
	return *updated, nil
}

// Delete removes one connection row. The soft-delete trigger captures the
// removed row into public.deleted_records; the foreign keys carry the
// linked accounts and the live flows with it.
func (r *Repository) Delete(ctx context.Context, db datastore.Querier, id uuid.UUID) (bool, error) {
	dbb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbb.DeleteFrom(entity.TableOAuthConnections)
	dbb.Where(dbb.Equal("id", id))
	query, args := dbb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ---- The flows ----

const flowColumns = `id, connection_id, state_hash, flow_token_hash, nonce, code_verifier,
	stage, user_id, email, email_code_hash, wrong_codes, provider_account_id,
	email_verified, given_name, family_name, profile, access_token, refresh_token,
	redirect_to, created_at, expires_at`

const linkedAccountColumns = `id, user_id, connection_id, provider_account_id, email,
	email_verified, profile, access_token, refresh_token, created_at, updated_at`

// maxWrongCodes is the three-strikes ceiling the email code's stage
// keeps — the same rule the other single-use bridges keep.
const maxWrongCodes = 3

// CreateFlow writes one pending ceremony row. The state hash is the only
// handle that exists yet — the flow token is minted at the callback, the
// moment the SPA first carries it.
func (r *Repository) CreateFlow(ctx context.Context, db datastore.Querier, connID uuid.UUID, stateHash, nonce, sealedVerifier string, expiresAt time.Time) (uuid.UUID, error) {
	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(entity.TableOAuthFlows)
	sb.Cols("connection_id", "state_hash", "nonce", "code_verifier", "stage", "expires_at")
	sb.Values(connID, stateHash, nonce, sealedVerifier, string(StagePending), expiresAt)
	sb.SQL("RETURNING id")
	query, args := sb.Build()

	var id uuid.UUID
	err := db.QueryRow(ctx, query, args...).Scan(&id)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return uuid.Nil(), fmt.Errorf("oauthsso: the flow's state collided with a live one")
		}
		return uuid.Nil(), err
	}
	return id, nil
}

// PendingByState reads the one pending ceremony the state names: the row
// the callback's browser carries, still un-consumed and inside its
// window. An unknown, spent, or expired state is the same not-found —
// the state is the credential, and a dead one answers nothing.
func (r *Repository) PendingByState(ctx context.Context, stateHash string) (Flow, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(flowColumns)
	sb.From(entity.TableOAuthFlows)
	sb.Where(
		sb.Equal("state_hash", stateHash),
		sb.Equal("stage", string(StagePending)),
		sb.IsNull("flow_token_hash"),
		sb.GreaterThan("expires_at", time.Now().UTC()),
	)
	query, args := sb.Build()
	return scanFlow(r.pool.QueryRow(ctx, query, args...))
}

// ConsumePending spends the pending ceremony on the identity the provider
// answered. The WHERE holds `pending` and the NULL flow token, so of two
// concurrent callbacks exactly one wins and the loser sees zero rows —
// the state is single-use. The fresh flow token's hash is written here,
// at the moment the SPA first carries it.
func (r *Repository) ConsumePending(ctx context.Context, db datastore.Querier, id uuid.UUID, resolution FlowResolution) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(entity.TableOAuthFlows)
	sb.Set(
		sb.Assign("stage", string(StageResolved)),
		sb.Assign("flow_token_hash", resolution.FlowTokenHash),
		sb.Assign("provider_account_id", resolution.ProviderAccountID),
		sb.Assign("email", resolution.Email),
		sb.Assign("email_verified", resolution.EmailVerified),
		sb.Assign("given_name", resolution.GivenName),
		sb.Assign("family_name", resolution.FamilyName),
		sb.Assign("profile", profileJSONFromBytes(resolution.Profile)),
		sb.Assign("access_token", resolution.SealedAccessToken),
		sb.Assign("refresh_token", resolution.SealedRefreshToken),
	)
	sb.Where(sb.Equal("id", id), sb.Equal("stage", string(StagePending)), sb.IsNull("flow_token_hash"))
	query, args := sb.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// LiveByFlowToken reads the ceremony the SPA's handle names, inside its
// window and not yet completed. The stages it may rest in are the
// caller's — the completion paths judge which ones they serve.
func (r *Repository) LiveByFlowToken(ctx context.Context, db datastore.Querier, tokenHash string, stages ...FlowStage) (Flow, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(flowColumns)
	sb.From(entity.TableOAuthFlows)
	sb.Where(sb.Equal("flow_token_hash", tokenHash), sb.GreaterThan("expires_at", time.Now().UTC()))
	if len(stages) > 0 {
		words := make([]any, 0, len(stages))
		for _, stage := range stages {
			words = append(words, string(stage))
		}
		sb.Where(sb.In("stage", words...))
	}
	query, args := sb.Build()
	return scanFlow(db.QueryRow(ctx, query, args...))
}

// DeleteExpiredFlows purges the ceremony rows past their expiry — the
// sweep's delete. Every read filters on a live expiry, so an expired row
// is unreachable before this sweep removes it.
func (r *Repository) DeleteExpiredFlows(ctx context.Context, now time.Time) (int64, error) {
	dbt := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbt.DeleteFrom(entity.TableOAuthFlows)
	dbt.Where(dbt.LessThan("expires_at", now))
	query, args := dbt.Build()
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ---- The resolution's reads and writes ----

// LinkedAccountByProvider reads the binding one connection holds for a
// provider account id — the resolution's first question. An absent row
// is datastore.ErrNoRows, the answer that sends the resolution down the
// link-or-create branches.
func (r *Repository) LinkedAccountByProvider(ctx context.Context, db datastore.Querier, connectionID uuid.UUID, providerAccountID string) (LinkedAccount, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(linkedAccountColumns)
	sb.From(entity.TableOAuthLinkedAccounts)
	sb.Where(sb.Equal("connection_id", connectionID), sb.Equal("provider_account_id", providerAccountID))
	query, args := sb.Build()
	return scanLinkedAccount(db.QueryRow(ctx, query, args...))
}

// CreateLinkedAccount binds one provider identity to an account. The
// caller's transaction owns the write, so the binding and whatever it
// rides — the account's creation or the session it opens — commit as one
// fact.
func (r *Repository) CreateLinkedAccount(ctx context.Context, db datastore.Querier, row LinkedAccount) error {
	row.ID = uuid.NewV7()
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableOAuthLinkedAccounts)
	ib.Cols("id", "user_id", "connection_id", "provider_account_id", "email", "email_verified", "profile", "access_token", "refresh_token")
	ib.Values(row.ID, row.UserID, row.ConnectionID, row.ProviderAccountID, row.Email,
		row.EmailVerified, profileJSONFromBytes(row.Profile), row.AccessToken, row.RefreshToken)
	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return err
	}
	return nil
}

// AccountIDByEmail reads the account an address names. The lookup runs
// over the users table alone — no join on the password — because a
// provider-verified address is itself the credential, and an account
// that holds no password is exactly the one an OAuth binding fits.
func (r *Repository) AccountIDByEmail(ctx context.Context, db datastore.Querier, email string) (uuid.UUID, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id")
	sb.From(entity.TableUsers)
	sb.Where(sb.Equal("email", email))
	query, args := sb.Build()
	var id uuid.UUID
	err := db.QueryRow(ctx, query, args...).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil(), datastore.ErrNoRows
	}
	if err != nil {
		return uuid.Nil(), err
	}
	return id, nil
}

// CreateAccount writes the JIT account: an address the provider verified
// (or the email code proved), the names the flow carries, and no
// password. The caller's transaction owns the write.
func (r *Repository) CreateAccount(ctx context.Context, db datastore.Querier, row user.UserSchema) (uuid.UUID, error) {
	row.ID = uuid.NewV7()
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUsers)
	ib.Cols("id", "username", "email", "first_name", "last_name", "display_name", "email_verified_at")
	ib.Values(row.ID, nullIfEmpty(row.Username), row.Email, nullIfEmpty(row.FirstName),
		nullIfEmpty(row.LastName), row.DisplayName, row.EmailVerifiedAt)
	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return uuid.Nil(), err
	}
	return row.ID, nil
}

// MoveToVerifyEmail pauses the flow for the email code: the stage flip
// is guarded on the stage it leaves, so a raced continue loses instead
// of double-sending, and the window widens by a full flow lifetime — the
// ceremony is actively driven now, not idling since its begin.
func (r *Repository) MoveToVerifyEmail(ctx context.Context, db datastore.Querier, id uuid.UUID, codeHash string, at time.Time) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(entity.TableOAuthFlows)
	sb.Set(
		sb.Assign("stage", string(StageVerifyEmail)),
		sb.Assign("email_code_hash", codeHash),
		sb.Assign("wrong_codes", 0),
		sb.Assign("expires_at", at),
	)
	sb.Where(sb.Equal("id", id), sb.Equal("stage", string(StageResolved)))
	query, args := sb.Build()
	return execAffected(ctx, db, query, args...)
}

// MoveToRequireNames pauses the flow for the names the provider did not
// supply, the same guarded flip the verify stage keeps.
func (r *Repository) MoveToRequireNames(ctx context.Context, db datastore.Querier, id uuid.UUID) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(entity.TableOAuthFlows)
	sb.Set(sb.Assign("stage", string(StageRequireNames)))
	sb.Where(sb.Equal("id", id), sb.Equal("stage", string(StageResolved)))
	query, args := sb.Build()
	return execAffected(ctx, db, query, args...)
}

// UpdateFlowNames stores the names the caller collected at the names
// stage. The guarded update answers whether the flow still rests there.
func (r *Repository) UpdateFlowNames(ctx context.Context, db datastore.Querier, id uuid.UUID, given, family string) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(entity.TableOAuthFlows)
	sb.Set(sb.Assign("given_name", given), sb.Assign("family_name", family))
	sb.Where(sb.Equal("id", id), sb.Equal("stage", string(StageRequireNames)))
	query, args := sb.Build()
	return execAffected(ctx, db, query, args...)
}

// SpendEmailCode consumes the verify_email stage: the guarded update
// carries the code's hash and the three-strikes ceiling in its WHERE, so
// a wrong code, an expired flow, and a raced repeat all answer the same
// "not spent". The address is marked verified — the code proved it — and
// the flow returns to the stage the resolution reads.
func (r *Repository) SpendEmailCode(ctx context.Context, db datastore.Querier, id uuid.UUID, codeHash string, next FlowStage) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(entity.TableOAuthFlows)
	sb.Set(
		sb.Assign("stage", string(next)),
		sb.Assign("email_verified", true),
		sb.Assign("email_code_hash", ""),
		sb.Assign("wrong_codes", 0),
	)
	sb.Where(sb.Equal("id", id), sb.Equal("stage", string(StageVerifyEmail)),
		sb.Equal("email_code_hash", codeHash), sb.LessThan("wrong_codes", maxWrongCodes))
	query, args := sb.Build()
	return execAffected(ctx, db, query, args...)
}

// StrikeEmailCode counts one wrong answer and reports whether the flow
// has spent its three. The increment rides a compare-and-set — the WHERE
// carries the count the caller read — so two racers cannot both land the
// same strike, and a loser's answer is the same "not spent".
func (r *Repository) StrikeEmailCode(ctx context.Context, db datastore.Querier, id uuid.UUID, strikes int) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(entity.TableOAuthFlows)
	sb.Set(sb.Assign("wrong_codes", strikes+1))
	sb.Where(sb.Equal("id", id), sb.Equal("stage", string(StageVerifyEmail)), sb.Equal("wrong_codes", strikes))
	query, args := sb.Build()
	landed, err := execAffected(ctx, db, query, args...)
	if err != nil || !landed {
		return false, err
	}
	return strikes+1 >= maxWrongCodes, nil
}

// FailFlow ends a flow that proved hostile — the three-strikes spend. The
// row stays for the sweep to collect, unreachable by every read.
func (r *Repository) FailFlow(ctx context.Context, db datastore.Querier, id uuid.UUID) error {
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(entity.TableOAuthFlows)
	sb.Set(sb.Assign("stage", string(StageCompleted)), sb.Assign("email_code_hash", ""))
	sb.Where(sb.Equal("id", id))
	query, args := sb.Build()
	_, err := db.Exec(ctx, query, args...)
	return err
}

// CompleteFlow spends the flow the resolution served: the account the
// session opened (or the bridge owed) is bound to the row, and the
// guarded update answers whether this continue was the winner.
func (r *Repository) CompleteFlow(ctx context.Context, db datastore.Querier, id uuid.UUID, userID uuid.UUID) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	sb.Update(entity.TableOAuthFlows)
	sb.Set(sb.Assign("stage", string(StageCompleted)), sb.Assign("user_id", userID))
	sb.Where(sb.Equal("id", id), sb.In("stage", string(StageResolved), string(StageRequireNames)))
	query, args := sb.Build()
	return execAffected(ctx, db, query, args...)
}

// execAffected answers whether the write found its row — the guarded
// updates' shared shape.
func execAffected(ctx context.Context, db datastore.Querier, query string, args ...any) (bool, error) {
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// scanLinkedAccountView renders one binding row beside its connection's
// slug. The scanner is the row/columns pair both reads share — a
// pgx.Rows and a pgx.Row satisfy the same shape.
func scanLinkedAccountView(row interface{ Scan(dest ...any) error }) (LinkedAccountView, error) {
	var view LinkedAccountView
	err := row.Scan(&view.LinkedAccount.ID, &view.LinkedAccount.UserID, &view.LinkedAccount.ConnectionID,
		&view.LinkedAccount.ProviderAccountID, &view.LinkedAccount.Email, &view.LinkedAccount.EmailVerified,
		&view.LinkedAccount.Profile, &view.LinkedAccount.AccessToken, &view.LinkedAccount.RefreshToken,
		&view.LinkedAccount.CreatedAt, &view.LinkedAccount.UpdatedAt, &view.Provider)
	if err != nil {
		return LinkedAccountView{}, err
	}
	return view, nil
}

// scanLinkedAccount renders one binding row onto the view. The sealed
// token columns stay as stored.
func scanLinkedAccount(row pgx.Row) (LinkedAccount, error) {
	var account LinkedAccount
	err := row.Scan(&account.ID, &account.UserID, &account.ConnectionID, &account.ProviderAccountID,
		&account.Email, &account.EmailVerified, &account.Profile, &account.AccessToken,
		&account.RefreshToken, &account.CreatedAt, &account.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return LinkedAccount{}, datastore.ErrNoRows
	}
	if err != nil {
		return LinkedAccount{}, err
	}
	return account, nil
}

// ---- The account surfaces ----

// LinkedAccountView carries one binding beside its connection's slug —
// the listing's word for "which provider this is".
type LinkedAccountView struct {
	LinkedAccount LinkedAccount
	Provider      string
}

// linkedAccountViewColumns is the binding list the two reads share:
// every column qualified with the binding's alias, and the connection's
// provider slug riding last.
const linkedAccountViewColumns = `l.id, l.user_id, l.connection_id, l.provider_account_id,
	l.email, l.email_verified, l.profile, l.access_token, l.refresh_token,
	l.created_at, l.updated_at, c.provider`

// LinkedAccountsByUser reads the caller's bindings, oldest first. The
// join carries the connection's slug; a connection deleted cascades its
// bindings away, so the inner join hides nothing.
func (r *Repository) LinkedAccountsByUser(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]LinkedAccountView, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(linkedAccountViewColumns)
	sb.From(entity.TableOAuthLinkedAccounts + " l")
	sb.Join(entity.TableOAuthConnections+" c", "c.id = l.connection_id")
	sb.Where(sb.Equal("l.user_id", userID))
	sb.OrderBy("l.created_at")
	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LinkedAccountView
	for rows.Next() {
		view, scanErr := scanLinkedAccountView(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, view)
	}
	return out, rows.Err()
}

// LinkedAccountByID reads one binding with its connection's slug.
func (r *Repository) LinkedAccountByID(ctx context.Context, db datastore.Querier, id uuid.UUID) (LinkedAccountView, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(linkedAccountViewColumns)
	sb.From(entity.TableOAuthLinkedAccounts + " l")
	sb.Join(entity.TableOAuthConnections+" c", "c.id = l.connection_id")
	sb.Where(sb.Equal("l.id", id))
	query, args := sb.Build()
	view, err := scanLinkedAccountView(db.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return LinkedAccountView{}, datastore.ErrNoRows
	}
	if err != nil {
		return LinkedAccountView{}, err
	}
	return view, nil
}

// KeepsAnotherCredential answers whether the account still holds a way
// in once this binding leaves: another binding, a password, or a
// passkey — the stranding rule's three sources. One scan answers all
// three, and the binding being removed counts itself out.
func (r *Repository) KeepsAnotherCredential(ctx context.Context, db datastore.Querier, userID, excludeID uuid.UUID) (bool, error) {
	var others int
	err := db.QueryRow(ctx, `SELECT
		    (SELECT count(*) FROM public.oauth_linked_accounts WHERE user_id = $1 AND id <> $2)
		  + (SELECT count(*) FROM public.user_passwords WHERE user_id = $1)
		  + (SELECT count(*) FROM public.webauthn_credentials WHERE user_id = $1)`, userID, excludeID).
		Scan(&others)
	if err != nil {
		return false, err
	}
	return others > 0, nil
}

// HoldsAlternativeCredential answers whether the account keeps a way in
// beside its password: an SSO binding or a passkey. The remove-password
// procedure's chained check asks through it — the same three-table rule
// KeepsAnotherCredential holds for the bindings, minus the password the
// removal is spending.
func (r *Repository) HoldsAlternativeCredential(ctx context.Context, db datastore.Querier, userID uuid.UUID) (bool, error) {
	var others int
	err := db.QueryRow(ctx, `SELECT
		    (SELECT count(*) FROM public.oauth_linked_accounts WHERE user_id = $1)
		  + (SELECT count(*) FROM public.webauthn_credentials WHERE user_id = $1)`, userID).
		Scan(&others)
	if err != nil {
		return false, err
	}
	return others > 0, nil
}

// DeleteLinkedAccount removes one binding. The guard carries the owner,
// so a raced or foreign delete answers "not removed" instead of
// touching someone else's row; the soft-delete trigger captures what
// the unlink removed.
func (r *Repository) DeleteLinkedAccount(ctx context.Context, db datastore.Querier, id, userID uuid.UUID) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	sb.DeleteFrom(entity.TableOAuthLinkedAccounts)
	sb.Where(sb.Equal("id", id), sb.Equal("user_id", userID))
	query, args := sb.Build()
	return execAffected(ctx, db, query, args...)
}

// scanFlow renders one row onto the view. The hash and sealed columns
// stay as stored — the callers unseal at their own boundary.
func scanFlow(row pgx.Row) (Flow, error) {
	var flow Flow
	err := row.Scan(&flow.ID, &flow.ConnectionID, &flow.StateHash, &flow.FlowTokenHash,
		&flow.Nonce, &flow.CodeVerifier, &flow.Stage, &flow.UserID, &flow.Email,
		&flow.EmailCodeHash, &flow.WrongCodes, &flow.ProviderAccountID,
		&flow.EmailVerified, &flow.GivenName, &flow.FamilyName, &flow.Profile,
		&flow.AccessToken, &flow.RefreshToken, &flow.RedirectTo, &flow.CreatedAt,
		&flow.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Flow{}, datastore.ErrNoRows
	}
	if err != nil {
		return Flow{}, err
	}
	return flow, nil
}

// profileJSONFromBytes passes the identity document through for its JSONB
// column: an empty document is the column's empty object, never NULL.
func profileJSONFromBytes(profile []byte) any {
	if len(profile) == 0 {
		return []byte(`{}`)
	}
	return profile
}

// scanConnection renders one row onto the view. The client secret rides
// exactly as stored — sealed — and every surface read drops it before the
// answer leaves the service.
func scanConnection(row pgx.Row) (Connection, error) {
	var (
		conn      Connection
		endpoints []byte
		scopes    []byte
		mapping   []byte
		discovery *string
	)
	err := row.Scan(&conn.ID, &conn.Kind, &conn.Provider, &conn.DisplayName, &discovery,
		&endpoints, &conn.ClientID, &conn.ClientSecret, &scopes, &mapping,
		&conn.Enabled, &conn.CreatedAt, &conn.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, ErrConnectionNotFound
	}
	if err != nil {
		return Connection{}, err
	}
	if discovery != nil {
		conn.DiscoveryURL = *discovery
	}
	if len(endpoints) > 0 {
		if err := json.Unmarshal(endpoints, &conn.Endpoints); err != nil {
			return Connection{}, fmt.Errorf("oauthsso: stored endpoints: %w", err)
		}
	}
	if len(scopes) > 0 {
		if err := json.Unmarshal(scopes, &conn.Scopes); err != nil {
			return Connection{}, fmt.Errorf("oauthsso: stored scopes: %w", err)
		}
	}
	if len(mapping) > 0 {
		var doc connectionMapping
		if err := json.Unmarshal(mapping, &doc); err != nil {
			return Connection{}, fmt.Errorf("oauthsso: stored attribute mapping: %w", err)
		}
		conn.AttributeMapping = doc.AttributeMapping
		conn.CustomAttributes = doc.CustomAttributes
	}
	return conn, nil
}

// nullIfEmpty renders an optional text column: an empty Go string is the
// column's NULL, never a stored empty value.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// endpointsJSON renders the endpoint set for its JSONB column. A zero
// struct — a builtin connection, whose endpoints are the code's own — is
// the column's NULL.
func endpointsJSON(e Endpoints) any {
	if e.Issuer == "" && e.Authorization == "" && e.Token == "" && e.Userinfo == "" && e.Jwks == "" {
		return nil
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	return raw
}

// scopesJSON renders the scope list for its JSONB column.
func scopesJSON(scopes []string) any {
	if scopes == nil {
		return []byte(`[]`)
	}
	raw, err := json.Marshal(scopes)
	if err != nil {
		return []byte(`[]`)
	}
	return raw
}

// connectionMapping is the attribute_mapping column's document: the
// mapping's own fields and the operator-defined attribute set together,
// so one JSONB column carries the whole mapping.
type connectionMapping struct {
	AttributeMapping
	CustomAttributes []CustomAttribute `json:"CustomAttributes,omitempty"`
}

// mappingJSON renders the attribute mapping for its JSONB column.
func mappingJSON(m AttributeMapping, attrs []CustomAttribute) any {
	raw, err := json.Marshal(connectionMapping{AttributeMapping: m, CustomAttributes: attrs})
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}
