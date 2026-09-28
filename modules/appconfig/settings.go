package appconfig

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/pkg/crypto"
)

// The failures the settings feature reports. The handler maps them to
// connect codes, so the wire form of a refusal lives with the transport,
// not here.
var (
	// ErrUnknownSetting is a key the table does not name.
	ErrUnknownSetting = errors.New("appconfig: unknown setting")

	// ErrSealedNotPublic is a write that asks for a row that is both sealed
	// and public. The unauthenticated read publishes every public row
	// verbatim, so the pair would hand a ciphertext — or worse, the promise
	// of one — to a caller before sign-in.
	ErrSealedNotPublic = errors.New("appconfig: a sealed setting cannot be public")

	// ErrSealUnavailable is a sensitive write on a process with no cipher:
	// no secret key is configured, so there is nothing to seal with.
	ErrSealUnavailable = errors.New("appconfig: no cipher is configured to seal a sensitive value")

	// ErrReservedPrefix is a plaintext write whose value begins with the
	// enc: marker. The prefix is how a read tells a sealed value from a
	// plain one, so a plain write may not forge it.
	ErrReservedPrefix = errors.New("appconfig: value begins with the reserved enc: prefix")

	// ErrMissingCipher is a sealed row a process without a cipher cannot
	// open. The read fails closed: ciphertext is never answered as the
	// value.
	ErrMissingCipher = errors.New("appconfig: setting rests sealed but no cipher is configured")
)

// Setting is one row in the clear: the value a caller wrote, whatever the
// table rests — a sealed row reads back the plaintext it was written with.
type Setting struct {
	Key       string
	Value     string
	Public    bool
	UpdatedAt *time.Time
}

// Settings is the database-backed settings feature: key/value rows for the
// product flows, editable at runtime, as distinct from the system
// configuration the JSON file owns.
//
// The values are strings. A value written sensitive rests sealed — the
// ciphertext carries the crypto package's enc: prefix — and every read,
// helper or RPC, opens one on the way out, so a caller never sees the
// sealed form. The cipher is the deployment's shared one; a process without
// a secret key carries none, and a sensitive write on such a process is
// refused rather than stored in the clear.
//
// Other features read their settings here: the getters take a context and
// a key and answer the value, the same door every reader uses. A change
// through the RPC surface leaves an audit record; the helper setters
// record nothing — the calling feature owns its own trail.
type Settings struct {
	pool   *datastore.Postgres
	cipher *crypto.Cipher
	audit  *audit.Recorder
}

// NewSettings builds the feature over the pool and the deployment's cipher.
// A nil cipher is a deployment without a secret key: reads and plain writes
// serve, sensitive writes are refused at the call site.
func NewSettings(pool *datastore.Postgres, cipher *crypto.Cipher, recorder *audit.Recorder) *Settings {
	return &Settings{pool: pool, cipher: cipher, audit: recorder}
}

// Get answers the value in the clear, or ErrUnknownSetting when the key is
// absent. A sealed value is opened on the way out.
func (s *Settings) Get(ctx context.Context, key string) (string, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("value")
	sb.From(SettingTable)
	sb.Where(sb.Equal("key", key))

	query, args := sb.Build()
	var stored string
	err := s.pool.QueryRow(ctx, query, args...).Scan(&stored)
	if errors.Is(err, datastore.ErrNoRows) {
		return "", ErrUnknownSetting
	}
	if err != nil {
		return "", fmt.Errorf("appconfig: read setting: %w", err)
	}
	return s.open(stored)
}

// GetString is Get under the name a typed caller reads it as. The value is
// stored as written, so a string setting needs no parse.
func (s *Settings) GetString(ctx context.Context, key string) (string, error) {
	return s.Get(ctx, key)
}

// GetBool is Get with the parse a bool caller wants. A value that does not
// parse is the caller's programming error and fails loudly rather than
// answering a zero.
func (s *Settings) GetBool(ctx context.Context, key string) (bool, error) {
	value, err := s.Get(ctx, key)
	if err != nil {
		return false, err
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("appconfig: setting %q is not a bool: %w", key, err)
	}
	return parsed, nil
}

// GetInt64 is Get with the parse a numeric caller wants, as loud as GetBool.
func (s *Settings) GetInt64(ctx context.Context, key string) (int64, error) {
	value, err := s.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("appconfig: setting %q is not an int: %w", key, err)
	}
	return parsed, nil
}

// GetSetting answers one row in the clear, or ErrUnknownSetting when the
// key is absent. The RPC Get reads through here, the same row the helpers'
// Get names.
func (s *Settings) GetSetting(ctx context.Context, key string) (Setting, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key", "value", "public", "created_at", "updated_at")
	sb.From(SettingTable)
	sb.Where(sb.Equal("key", key))

	query, args := sb.Build()
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return Setting{}, fmt.Errorf("appconfig: read setting: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return Setting{}, fmt.Errorf("appconfig: read setting: %w", err)
		}
		return Setting{}, ErrUnknownSetting
	}
	setting, err := s.scanSetting(rows)
	if err != nil {
		return Setting{}, err
	}
	return setting, nil
}

// Set writes the value in the clear, sealing it first when sensitive. The
// write is an upsert: the key may already rest, and the row's flags are
// exactly the ones the call carries. It records nothing — the calling
// feature owns its audit trail.
func (s *Settings) Set(ctx context.Context, key, value string, sensitive, public bool) error {
	_, err := s.store(ctx, s.pool, key, value, sensitive, public)
	return err
}

// Delete removes the row, or fails with ErrUnknownSetting when the key is
// absent. It records nothing, like Set.
func (s *Settings) Delete(ctx context.Context, key string) error {
	db := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	db.DeleteFrom(SettingTable)
	db.Where(db.Equal("key", key))

	query, args := db.Build()
	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("appconfig: delete setting: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUnknownSetting
	}
	return nil
}

// List answers every setting for the administrator, ordered by key, every
// value in the clear. The sealed rows are opened on the way out, the way
// Get reads one.
func (s *Settings) List(ctx context.Context) ([]Setting, error) {
	return s.list(ctx, false)
}

// ListPublic answers the rows an unauthenticated client may read: the ones
// flagged public, verbatim. A public row never rests sealed — the write
// refuses the pair and the table checks it — so there is nothing to open.
func (s *Settings) ListPublic(ctx context.Context) ([]Setting, error) {
	return s.list(ctx, true)
}

func (s *Settings) list(ctx context.Context, publicOnly bool) ([]Setting, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key", "value", "public", "created_at", "updated_at")
	sb.From(SettingTable)
	if publicOnly {
		sb.Where(sb.Equal("public", true))
	}
	sb.OrderBy("key")

	query, args := sb.Build()
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("appconfig: list settings: %w", err)
	}
	defer rows.Close()

	settings := []Setting{}
	for rows.Next() {
		setting, err := s.scanSetting(rows)
		if err != nil {
			return nil, err
		}
		settings = append(settings, setting)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("appconfig: list settings: %w", err)
	}
	return settings, nil
}

// SetFor is the RPC surface's write: the upsert with the audit record of
// the change inside the same transaction, so a record never describes a
// change that rolled back. The payload names the key and the flags — never
// the value, which may be a secret.
func (s *Settings) SetFor(ctx context.Context, callerID string, key, value string, sensitive, public bool) (Setting, error) {
	var setting Setting
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		written, err := s.store(ctx, tx, key, value, sensitive, public)
		if err != nil {
			return err
		}
		setting = written

		payload := map[string]string{"key": key}
		if sensitive {
			payload["sealed"] = "true"
		}
		if public {
			payload["public"] = "true"
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventSettingUpdated,
			Status: audit.StatusSuccess,
			UserID: callerID,
			// The key is not a UUID, so it cannot name the resource column —
			// the payload carries it, with the flags and never the value.
			Payload: payload,
		})
		return nil
	})
	return setting, err
}

// DeleteFor is the RPC surface's delete: the removal with the audit record
// inside the same transaction.
func (s *Settings) DeleteFor(ctx context.Context, callerID, key string) error {
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		db := sqlbuilder.PostgreSQL.NewDeleteBuilder()
		db.DeleteFrom(SettingTable)
		db.Where(db.Equal("key", key))

		query, args := db.Build()
		tag, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("appconfig: delete setting: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrUnknownSetting
		}

		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventSettingDeleted,
			Status: audit.StatusSuccess,
			UserID: callerID,
			// The key is not a UUID — the payload carries it.
			Payload: map[string]string{"key": key},
		})
		return nil
	})
}

// store upserts the row. The value is sealed here, on the way in, when the
// write says so — the table never learns the plaintext of a sensitive row.
func (s *Settings) store(ctx context.Context, q datastore.Querier, key, value string, sensitive, public bool) (Setting, error) {
	if sensitive && public {
		return Setting{}, ErrSealedNotPublic
	}
	if !sensitive && strings.HasPrefix(value, crypto.EncPrefix) {
		return Setting{}, ErrReservedPrefix
	}

	stored := value
	if sensitive {
		if s.cipher == nil {
			return Setting{}, ErrSealUnavailable
		}
		sealed, err := s.cipher.Encrypt(value)
		if err != nil {
			return Setting{}, fmt.Errorf("appconfig: seal setting: %w", err)
		}
		stored = sealed
	}

	sb := sqlbuilder.PostgreSQL.NewInsertBuilder()
	sb.InsertInto(SettingTable)
	sb.Cols("key", "value", "public")
	sb.Values(key, stored, public)
	sb.SQL("ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, public = EXCLUDED.public")
	sb.Returning("created_at", "updated_at")

	query, args := sb.Build()
	setting := Setting{Key: key, Value: value, Public: public}
	var createdAt time.Time
	err := q.QueryRow(ctx, query, args...).Scan(&createdAt, &setting.UpdatedAt)
	if err != nil {
		return Setting{}, fmt.Errorf("appconfig: write setting: %w", err)
	}
	return setting, nil
}

// open turns a stored value into the value in the clear. The enc: prefix
// is the whole story: a value wearing it rests sealed, a value without it
// is the plaintext itself.
func (s *Settings) open(stored string) (string, error) {
	if !strings.HasPrefix(stored, crypto.EncPrefix) {
		return stored, nil
	}
	if s.cipher == nil {
		return "", ErrMissingCipher
	}
	value, err := s.cipher.Decrypt(stored)
	if err != nil {
		return "", fmt.Errorf("appconfig: open setting: %w", err)
	}
	return value, nil
}

// scanSetting lifts one row into the clear. A sealed value is opened here,
// so every listing path shares the read.
func (s *Settings) scanSetting(rows pgx.Rows) (Setting, error) {
	var schema SettingSchema
	if err := rows.Scan(&schema.Key, &schema.Value, &schema.Public, &schema.CreatedAt, &schema.UpdatedAt); err != nil {
		return Setting{}, fmt.Errorf("appconfig: scan setting: %w", err)
	}
	value, err := s.open(schema.Value)
	if err != nil {
		return Setting{}, err
	}
	return Setting{
		Key:       schema.Key,
		Value:     value,
		Public:    schema.Public,
		UpdatedAt: schema.UpdatedAt,
	}, nil
}
