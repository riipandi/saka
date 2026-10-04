package webauthn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/database/entity"
)

// ErrNoRows re-exports the datastore's sentinel so callers answer one
// not-found across packages.
var ErrNoRows = datastore.ErrNoRows

// Repository is the webauthn tables' reader and writer. Every method takes
// the query surface as an argument, so a caller holding an open transaction
// keeps its writes atomic with whatever caused them.
type Repository struct{}

// NewRepository builds the repository.
func NewRepository() *Repository { return &Repository{} }

// ---- Enrolled credentials ----

// credentialColumns is the select list one credential row answers, in the
// scan order scanCredential reads.
var credentialColumns = []string{
	"id", "user_id", "name", "credential_id", "public_key", "sign_count",
	"attestation_type", "transport", "backup_eligible", "backup_state",
	"aaguid", "created_at", "updated_at", "last_used_at",
}

// scanCredential scans one credential row. The id arrives as text — the wire
// form — and leaves parsed.
func scanCredential(scan func(dest ...any) error) (CredentialSchema, error) {
	var row CredentialSchema
	var rawID string
	err := scan(&rawID, &row.UserID, &row.Name, &row.CredentialID, &row.PublicKey, &row.SignCount,
		&row.AttestationType, &row.Transport, &row.BackupEligible, &row.BackupState,
		&row.AAGUID, &row.CreatedAt, &row.UpdatedAt, &row.LastUsedAt)
	if err != nil {
		return CredentialSchema{}, err
	}
	parsed, parseErr := uuid.Parse(rawID)
	if parseErr != nil {
		return CredentialSchema{}, fmt.Errorf("webauthn: id: %w", parseErr)
	}
	row.ID = parsed
	return row, nil
}

// CreateCredential writes one enrolled passkey. The service fills the
// identifier and every parsed attestation field. The credential_id's
// uniqueness is the protocol's — one authenticator, one account — so a
// duplicate insert is the duplicate-enrollment refusal, not an internal
// failure.
func (r *Repository) CreateCredential(ctx context.Context, db datastore.Querier, row CredentialSchema) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableWebauthnCredentials)
	ib.Cols("id", "user_id", "name", "credential_id", "public_key", "sign_count",
		"attestation_type", "transport", "backup_eligible", "backup_state", "aaguid", "created_at")
	ib.Values(row.ID, row.UserID, row.Name, row.CredentialID, row.PublicKey, row.SignCount,
		row.AttestationType, row.Transport, row.BackupEligible, row.BackupState, row.AAGUID, row.CreatedAt)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrCredentialDuplicate
		}
		return fmt.Errorf("webauthn: create credential: %w", err)
	}
	return nil
}

// GetCredentialByID reads one credential by its row identifier, whatever
// account holds it. The service scopes the read to the caller before
// answering.
func (r *Repository) GetCredentialByID(ctx context.Context, db datastore.Querier, id uuid.UUID) (CredentialSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(credentialColumns...)
	sb.From(entity.TableWebauthnCredentials)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	row, err := scanCredential(func(dest ...any) error { return db.QueryRow(ctx, query, args...).Scan(dest...) })
	if errors.Is(err, datastore.ErrNoRows) {
		return CredentialSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return CredentialSchema{}, fmt.Errorf("webauthn: get credential: %w", err)
	}
	return row, nil
}

// GetCredentialByCredentialID reads one credential by the authenticator's
// raw credential id — the key usernameless sign-in resolves the account by.
func (r *Repository) GetCredentialByCredentialID(ctx context.Context, db datastore.Querier, credentialID []byte) (CredentialSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(credentialColumns...)
	sb.From(entity.TableWebauthnCredentials)
	sb.Where(sb.Equal("credential_id", credentialID))

	query, args := sb.Build()
	row, err := scanCredential(func(dest ...any) error { return db.QueryRow(ctx, query, args...).Scan(dest...) })
	if errors.Is(err, datastore.ErrNoRows) {
		return CredentialSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return CredentialSchema{}, fmt.Errorf("webauthn: get credential by id: %w", err)
	}
	return row, nil
}

// ListCredentials reads every credential the account holds, oldest first —
// the order a settings page renders.
func (r *Repository) ListCredentials(ctx context.Context, db datastore.Querier, userID uuid.UUID) ([]CredentialSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select(credentialColumns...)
	sb.From(entity.TableWebauthnCredentials)
	sb.Where(sb.Equal("user_id", userID))
	sb.OrderBy("created_at")

	query, args := sb.Build()
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("webauthn: list credentials: %w", err)
	}
	defer rows.Close()

	var out []CredentialSchema
	for rows.Next() {
		row, scanErr := scanCredential(rows.Scan)
		if scanErr != nil {
			return nil, fmt.Errorf("webauthn: list credentials: %w", scanErr)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("webauthn: list credentials: %w", err)
	}
	return out, nil
}

// CountCredentials answers how many passkeys the account holds — the number
// the passkey.max_credentials limit is judged against.
func (r *Repository) CountCredentials(ctx context.Context, db datastore.Querier, userID uuid.UUID) (int, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("count(*)")
	sb.From(entity.TableWebauthnCredentials)
	sb.Where(sb.Equal("user_id", userID))

	query, args := sb.Build()
	var count int
	if err := db.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("webauthn: count credentials: %w", err)
	}
	return count, nil
}

// RenameCredential replaces a credential's display name.
func (r *Repository) RenameCredential(ctx context.Context, db datastore.Querier, id uuid.UUID, name string) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableWebauthnCredentials)
	ub.Set(ub.Assign("name", name))
	ub.Where(ub.Equal("id", id))

	query, args := ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("webauthn: rename credential: %w", err)
	}
	return nil
}

// RecordAssertion is the bookkeeping one verified assertion produces: the
// advanced signature counter, the backup state the authenticator now
// reports, and the last-use stamp. The counter's update carries its own
// guard — it moves only forward — so two assertions verifying against the
// same stored count cannot commit a regression whichever commits last: the
// backward writer is a no-op, and the clone signal stays intact. The other
// columns are unconditional; the caller's transaction keeps them together
// where one exists.
func (r *Repository) RecordAssertion(ctx context.Context, db datastore.Querier, id uuid.UUID, signCount int64, backupState bool, usedAt time.Time) error {
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableWebauthnCredentials)
	ub.Set(
		ub.Assign("sign_count", signCount),
		ub.Assign("backup_state", backupState),
		ub.Assign("last_used_at", usedAt),
	)
	ub.Where(ub.Equal("id", id), ub.LessThan("sign_count", signCount))
	query, args := ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("webauthn: record assertion: %w", err)
	}

	ub = sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(entity.TableWebauthnCredentials)
	ub.Set(
		ub.Assign("backup_state", backupState),
		ub.Assign("last_used_at", usedAt),
	)
	ub.Where(ub.Equal("id", id))
	query, args = ub.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("webauthn: record assertion: %w", err)
	}
	return nil
}

// DeleteCredential removes one credential. It answers whether the row
// existed, so the service can tell an already-gone credential from a
// foreign one.
func (r *Repository) DeleteCredential(ctx context.Context, db datastore.Querier, id uuid.UUID) (bool, error) {
	dbt := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbt.DeleteFrom(entity.TableWebauthnCredentials)
	dbt.Where(dbt.Equal("id", id))

	query, args := dbt.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("webauthn: delete credential: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteExpiredTokens removes every token row past its expiry and answers
// how many it reaped — the sweep the maintenance job calls. An expired token
// is refused on read already; the sweep is what keeps the table from
// keeping the refused rows forever, whatever purpose minted them.
func (r *Repository) DeleteExpiredTokens(ctx context.Context, db datastore.Querier, now time.Time) (int64, error) {
	dbt := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbt.DeleteFrom(entity.TableAuthTokens)
	dbt.Where(dbt.LessThan("expires_at", now))

	query, args := dbt.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("webauthn: delete expired tokens: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ---- Ceremony sessions ----

// CreateSession writes one live ceremony row. The service fills the
// identifier, the challenge, and every parsed option.
func (r *Repository) CreateSession(ctx context.Context, db datastore.Querier, row SessionSchema) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableWebauthnSessions)
	ib.Cols("id", "user_id", "challenge", "challenge_type", "user_verification", "credential_params", "extensions", "created_at", "expires_at")
	ib.Values(row.ID, row.UserID, row.Challenge, row.ChallengeType, row.UserVerification, row.CredentialParams, row.Extensions, row.CreatedAt, row.ExpiresAt)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("webauthn: create session: %w", err)
	}
	return nil
}

// GetSession reads one ceremony row by its identifier. The challenge column
// carries the uniqueness the protocol needs; the identifier is only the
// handle the client echoes back.
func (r *Repository) GetSession(ctx context.Context, db datastore.Querier, id uuid.UUID) (SessionSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id", "user_id", "challenge", "challenge_type", "user_verification", "credential_params", "extensions", "created_at", "expires_at")
	sb.From(entity.TableWebauthnSessions)
	sb.Where(sb.Equal("id", id))

	query, args := sb.Build()
	row, err := scanSession(func(dest ...any) error { return db.QueryRow(ctx, query, args...).Scan(dest...) })
	if errors.Is(err, datastore.ErrNoRows) {
		return SessionSchema{}, datastore.ErrNoRows
	}
	if err != nil {
		return SessionSchema{}, fmt.Errorf("webauthn: get session: %w", err)
	}
	return row, nil
}

// ConsumeSession deletes one live ceremony row in the same statement that
// proves it: single use is the DELETE's WHERE, so a replayed ceremony handle
// names no row and an expired one is spent by the same refusal. It answers
// whether the row was live.
func (r *Repository) ConsumeSession(ctx context.Context, db datastore.Querier, id uuid.UUID, now time.Time) (bool, error) {
	dbt := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbt.DeleteFrom(entity.TableWebauthnSessions)
	dbt.Where(dbt.Equal("id", id), dbt.GreaterThan("expires_at", now))

	query, args := dbt.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("webauthn: consume session: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteExpiredSessions removes every ceremony row past its expiry and
// answers how many it reaped — the sweep the cleanup job calls on its own
// schedule.
func (r *Repository) DeleteExpiredSessions(ctx context.Context, db datastore.Querier, now time.Time) (int64, error) {
	dbt := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbt.DeleteFrom(entity.TableWebauthnSessions)
	dbt.Where(dbt.LessThan("expires_at", now))

	query, args := dbt.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("webauthn: delete expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// scanSession scans one ceremony row. The id arrives as text — the wire
// form — and leaves parsed.
func scanSession(scan func(dest ...any) error) (SessionSchema, error) {
	var row SessionSchema
	var rawID string
	err := scan(&rawID, &row.UserID, &row.Challenge, &row.ChallengeType, &row.UserVerification,
		&row.CredentialParams, &row.Extensions, &row.CreatedAt, &row.ExpiresAt)
	if err != nil {
		return SessionSchema{}, err
	}
	parsed, parseErr := uuid.Parse(rawID)
	if parseErr != nil {
		return SessionSchema{}, fmt.Errorf("webauthn: session id: %w", parseErr)
	}
	row.ID = parsed
	return row, nil
}

// ---- Step-up tokens ----

// CreateReauthenticationToken writes one hashed step-up token. Several live
// tokens per account are legitimate — the unique index excludes this
// purpose — and consumption is the single-use UPDATE below. The clock is
// the caller's: the service owns the time a test can pin.
func (r *Repository) CreateReauthenticationToken(ctx context.Context, db datastore.Querier, userID uuid.UUID, tokenHash string, createdAt, expiresAt time.Time) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableAuthTokens)
	ib.Cols("id", "user_id", "token_hash", "purpose", "created_at", "expires_at")
	ib.Values(uuid.NewV7(), userID, tokenHash, "reauthentication", createdAt, expiresAt)

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("webauthn: create reauthentication token: %w", err)
	}
	return nil
}

// ConsumeReauthenticationToken spends one step-up token: single use is the
// DELETE's WHERE, which carries the token hash, the account the caller is,
// the purpose, and the window — so a replayed, foreign, or expired token
// deletes nothing and answers false. The audit record rides the caller's
// transaction, not this write.
func (r *Repository) ConsumeReauthenticationToken(ctx context.Context, db datastore.Querier, userID uuid.UUID, tokenHash string, now time.Time) (bool, error) {
	dbt := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbt.DeleteFrom(entity.TableAuthTokens)
	dbt.Where(
		dbt.Equal("token_hash", tokenHash),
		dbt.Equal("user_id", userID),
		dbt.Equal("purpose", "reauthentication"),
		dbt.GreaterThan("expires_at", now),
	)

	query, args := dbt.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("webauthn: consume reauthentication token: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// UpsertReauthenticationCode writes the one live email-code row: the
// purpose sits under the partial unique index, so resend replaces — the
// newest code is the only one that works. The clock is the caller's.
func (r *Repository) UpsertReauthenticationCode(ctx context.Context, db datastore.Querier, userID uuid.UUID, tokenHash string, expiresAt, sentAt time.Time) error {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableAuthTokens)
	ib.Cols("id", "user_id", "token_hash", "purpose", "expires_at", "last_sent_at")
	ib.Values(uuid.NewV7(), userID, tokenHash, "reauthentication_code", expiresAt, sentAt)
	// The conflict target carries the partial index's predicate: the unique
	// index excludes reauthentication, so a bare column list matches nothing.
	ib.SQL("ON CONFLICT (user_id, purpose) WHERE purpose <> 'reauthentication' DO UPDATE SET " +
		"token_hash = EXCLUDED.token_hash, " +
		"expires_at = EXCLUDED.expires_at, " +
		"last_sent_at = EXCLUDED.last_sent_at")

	query, args := ib.Build()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("webauthn: upsert reauthentication code: %w", err)
	}
	return nil
}

// ConsumeReauthenticationCode spends one email code: single use is the
// DELETE's WHERE — the hash, the account, the purpose, the window. A wrong,
// foreign, or expired code deletes nothing and answers false.
func (r *Repository) ConsumeReauthenticationCode(ctx context.Context, db datastore.Querier, userID uuid.UUID, tokenHash string, now time.Time) (bool, error) {
	dbt := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	dbt.DeleteFrom(entity.TableAuthTokens)
	dbt.Where(
		dbt.Equal("token_hash", tokenHash),
		dbt.Equal("user_id", userID),
		dbt.Equal("purpose", "reauthentication_code"),
		dbt.GreaterThan("expires_at", now),
	)

	query, args := dbt.Build()
	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("webauthn: consume reauthentication code: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ReauthenticationCodeSentAt reads the live code row's last send, the stamp
// the resend cooldown judges. No row is an account that never asked.
func (r *Repository) ReauthenticationCodeSentAt(ctx context.Context, db datastore.Querier, userID uuid.UUID) (*time.Time, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("last_sent_at")
	sb.From(entity.TableAuthTokens)
	sb.Where(
		sb.Equal("user_id", userID),
		sb.Equal("purpose", "reauthentication_code"),
	)

	query, args := sb.Build()
	row := db.QueryRow(ctx, query, args...)
	var sentAt *time.Time
	if err := row.Scan(&sentAt); err != nil {
		if errors.Is(err, datastore.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("webauthn: read reauthentication code stamp: %w", err)
	}
	return sentAt, nil
}
