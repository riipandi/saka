package appconfig

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/cache"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/pkg/crypto"
)

// The failures the settings feature reports. The handler maps them to
// connect codes, so the wire form of a refusal lives with the transport,
// not here.
var (
	// ErrUnknownSetting is a key the catalog does not declare. Settings are
	// declared in code — the catalog owns the keys, the defaults, and the
	// flags — so a key that names nothing is a caller's mistake, not a new
	// item waiting to be created.
	ErrUnknownSetting = errors.New("appconfig: unknown setting")

	// ErrSealUnavailable is a sensitive write on a process with no cipher:
	// no secret key is configured, so there is nothing to seal with.
	ErrSealUnavailable = errors.New("appconfig: no cipher is configured to seal a sensitive value")

	// ErrReservedPrefix is a plain write whose value begins with the enc:
	// marker. The prefix is how a read tells a sealed value from a plain
	// one, so a plain write may not forge it.
	ErrReservedPrefix = errors.New("appconfig: value begins with the reserved enc: prefix")

	// ErrMissingCipher is a sealed row a process without a cipher cannot
	// open. The read fails closed: ciphertext is never answered as the
	// value.
	ErrMissingCipher = errors.New("appconfig: setting rests sealed but no cipher is configured")

	// ErrInvalidCatalog is a catalog that contradicts itself. The public
	// read publishes every public item's value verbatim, so a public item
	// that rests sealed would hand a ciphertext to a caller before sign-in.
	ErrInvalidCatalog = errors.New("appconfig: a public setting cannot rest sealed")
)

// SettingDef is one catalog entry: the declaration of a setting the product
// flows read. The code owns it — the key, the value a reset restores, and
// the flags the surface serves it with — while the table holds only the
// overrides.
type SettingDef struct {
	Key         string
	Default     string
	Sealed      bool
	Public      bool
	Description string
}

// Catalog declares every setting this deployment knows. It is the contract
// between the writers and the readers: a feature that wants a runtime value
// names its key here, and every reader — helper or RPC — sees the same item
// with the same default.
func Catalog() []SettingDef {
	return []SettingDef{
		{
			Key:         "product.name",
			Default:     "Tango",
			Public:      true,
			Description: "The product name the application shows.",
		},
		{
			Key:         "product.support_email",
			Default:     "",
			Public:      true,
			Description: "The support address the product flows surface. Empty means none.",
		},
		{
			Key:         "product.announcement",
			Default:     "",
			Public:      true,
			Description: "The announcement banner text. Empty means no banner.",
		},
	}
}

// Setting is one catalog item with its effective value in the clear. A
// sealed item's stored form is ciphertext; a reader never sees it.
type Setting struct {
	Key       string
	Value     string
	Default   string
	Sealed    bool
	Public    bool
	UpdatedAt *time.Time
}

// Settings is the database-backed settings feature: catalog items whose
// overrides rest in the database, editable at runtime, as distinct from the
// system configuration the JSON file owns.
//
// The values are strings. A sealed item's value rests encrypted — the
// ciphertext carries the crypto package's enc: prefix — and every read,
// helper or RPC, opens one on the way out, so a caller never sees the
// sealed form. The cipher is the deployment's shared one; a process without
// a secret key carries none, and a sealed write on such a process is
// refused rather than stored in the clear.
//
// Other features read their settings here: the getters take a context and
// a key and answer the effective value — the override when one rests, the
// catalog default when not. A change through the RPC surface leaves an
// audit record; the helper setters record nothing — the calling feature
// owns its own trail.
type Settings struct {
	pool   *datastore.Postgres
	cipher *crypto.Cipher
	audit  *audit.Recorder
	// defs is the catalog ordered by key — the order every listing answers
	// in — and byKey is the lookup the reads and writes resolve with.
	defs  []SettingDef
	byKey map[string]SettingDef

	// cache carries the reads the surface serves from: the public listing
	// and the effective value of an unsealed item. It is the shared Cache
	// the registry builds — Noop while caching is off, so a feature never
	// branches on the question — and nil only in a test that built the
	// feature by hand, which the reads below answer uncached.
	cache cache.Cache
}

// Cache keys the settings reads store under. The public listing is one
// document; a gate's value rests one entry per catalog key, so a change
// invalidates the one item it touched instead of the family.
const (
	listPublicCacheKey = "settings:public"
	gateCacheKeyPrefix = "settings:gate:"
)

// NewSettings builds the feature over the pool and the deployment's cipher.
// A nil cipher is a deployment without a secret key: reads and plain writes// serve, sealed writes are refused at the call site. A catalog that asks
// for the impossible — a public item that rests sealed — is refused here,
// so the run fails before the surface opens.
func NewSettings(pool *datastore.Postgres, cipher *crypto.Cipher, recorder *audit.Recorder, cache cache.Cache) (*Settings, error) {
	return newSettings(pool, cipher, recorder, Catalog(), cache)
}

// newSettings builds the feature over an explicit catalog. It is the
// constructor's body, separate so a test can drive a catalog the shipped
// one does not carry. A nil cache serves every read uncached.
func newSettings(pool *datastore.Postgres, cipher *crypto.Cipher, recorder *audit.Recorder, defs []SettingDef, cache cache.Cache) (*Settings, error) {
	ordered := slices.Clone(defs)
	slices.SortFunc(ordered, func(a, b SettingDef) int { return strings.Compare(a.Key, b.Key) })
	settings := &Settings{
		pool: pool, cipher: cipher, audit: recorder, cache: cache,
		defs: ordered, byKey: map[string]SettingDef{},
	}
	for _, def := range ordered {
		if def.Public && def.Sealed {
			return nil, ErrInvalidCatalog
		}
		if _, clash := settings.byKey[def.Key]; clash {
			return nil, fmt.Errorf("%w: %s", ErrInvalidCatalog, def.Key)
		}
		settings.byKey[def.Key] = def
	}
	return settings, nil
}

// Get answers the effective value in the clear, or ErrUnknownSetting when
// the key is not in the catalog. A sealed value is opened on the way out.
//
// An unsealed item's effective value is read through the cache: the gates a
// feature checks per request — a feature flag, an enabled flow — pay one
// query per window instead of one per call. A sealed item is never cached:
// the value it opens is a secret, and the cache's backends outlive a
// request. The reads a change brings also age out on their own — Update and
// Reset drop the entries the change touches.
func (s *Settings) Get(ctx context.Context, key string) (string, error) {
	def, ok := s.byKey[key]
	if !ok {
		return "", ErrUnknownSetting
	}
	if def.Sealed || s.cache == nil {
		setting, err := s.GetSetting(ctx, key)
		if err != nil {
			return "", err
		}
		return setting.Value, nil
	}
	return cache.Fetch(ctx, s.cache, gateCacheKeyPrefix+key, 0, false, func(ctx context.Context) (string, error) {
		setting, err := s.GetSetting(ctx, key)
		if err != nil {
			return "", err
		}
		return setting.Value, nil
	})
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

// GetSetting answers one catalog item with its effective value: the
// override when one rests, the default when not.
func (s *Settings) GetSetting(ctx context.Context, key string) (Setting, error) {
	def, ok := s.byKey[key]
	if !ok {
		return Setting{}, ErrUnknownSetting
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key", "value", "created_at", "updated_at")
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
		return s.atDefault(def), nil
	}
	return s.scanSetting(def, rows)
}

// Update writes the value in the clear, sealing it first when the catalog
// says the item rests sealed. The write is an upsert: the override may
// already rest, and the item's flags are the catalog's, not the call's. It
// records nothing — the calling feature owns its audit trail.
func (s *Settings) Update(ctx context.Context, key, value string) error {
	if _, err := s.store(ctx, s.pool, key, value); err != nil {
		return err
	}
	s.invalidate(ctx, key)
	return nil
}

// Reset removes the override, so the item answers its catalog default
// again. An item with no override resting is already at its default, so
// the reset answers the item unchanged. It records nothing, like Update.
func (s *Settings) Reset(ctx context.Context, key string) error {
	if _, ok := s.byKey[key]; !ok {
		return ErrUnknownSetting
	}

	db := sqlbuilder.PostgreSQL.NewDeleteBuilder()
	db.DeleteFrom(SettingTable)
	db.Where(db.Equal("key", key))

	query, args := db.Build()
	if _, err := s.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("appconfig: reset setting: %w", err)
	}
	s.invalidate(ctx, key)
	return nil
}

// List answers every catalog item with its effective values, ordered by
// key. The sealed items are opened on the way out, the way Get reads one.
func (s *Settings) List(ctx context.Context) ([]Setting, error) {
	overrides, err := s.overrides(ctx, false)
	if err != nil {
		return nil, err
	}

	settings := make([]Setting, 0, len(s.defs))
	for _, def := range s.defs {
		override, resting := overrides[def.Key]
		if !resting {
			settings = append(settings, s.atDefault(def))
			continue
		}
		setting, err := s.fromRow(def, override)
		if err != nil {
			return nil, err
		}
		settings = append(settings, setting)
	}
	return settings, nil
}

// ListPublic answers the catalog items an unauthenticated client may read:
// the ones flagged public, their effective values. A public item never
// rests sealed — the catalog refuses the pair — so there is nothing to
// open here that the catalog has not already promised is plain.
//
// The answer is one document every caller reads the same, so it is served
// from one cached entry; a change through Update or Reset drops it. A
// bypassed call reads the source and leaves the cached entry alone.
func (s *Settings) ListPublic(ctx context.Context) ([]Setting, error) {
	return s.ListPublicBypassingCache(ctx, false)
}

// ListPublicBypassingCache is ListPublic with the caller-facing bypass: a
// request that carries the flag reads the source without disturbing what
// the cache holds.
func (s *Settings) ListPublicBypassingCache(ctx context.Context, bypass bool) ([]Setting, error) {
	if s.cache == nil {
		return s.listPublic(ctx)
	}
	return cache.Fetch(ctx, s.cache, listPublicCacheKey, 0, bypass, s.listPublic)
}

// listPublic is ListPublic's own read.
func (s *Settings) listPublic(ctx context.Context) ([]Setting, error) {
	overrides, err := s.overrides(ctx, true)
	if err != nil {
		return nil, err
	}

	settings := []Setting{}
	for _, def := range s.defs {
		if !def.Public {
			continue
		}
		override, resting := overrides[def.Key]
		if !resting {
			settings = append(settings, s.atDefault(def))
			continue
		}
		setting, err := s.fromRow(def, override)
		if err != nil {
			return nil, err
		}
		settings = append(settings, setting)
	}
	return settings, nil
}

// invalidate drops the cache entries a change to one setting touches: the
// public listing that carries it and the gate value the features read. The
// call runs after the write commits, so a cache that answers again is the
// database's answer.
func (s *Settings) invalidate(ctx context.Context, key string) {
	if s.cache == nil {
		return
	}
	s.cache.Del(ctx, listPublicCacheKey)
	s.cache.Del(ctx, gateCacheKeyPrefix+key)
}

// UpdateFor is the RPC surface's write: the upsert with the audit record of
// the change inside the same transaction, so a record never describes a
// change that rolled back. The payload names the key and the flags — never
// the value, which may be a secret.
func (s *Settings) UpdateFor(ctx context.Context, callerID, key, value string) (Setting, error) {
	def, ok := s.byKey[key]
	if !ok {
		return Setting{}, ErrUnknownSetting
	}

	var setting Setting
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		written, err := s.store(ctx, tx, key, value)
		if err != nil {
			return err
		}
		setting = written

		payload := map[string]string{"key": key}
		if def.Sealed {
			payload["sealed"] = "true"
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
	if err != nil {
		return Setting{}, err
	}
	s.invalidate(ctx, key)
	return setting, nil
}

// ResetFor is the RPC surface's reset: the override is removed and the item
// answers its catalog default again. A reset that changed something leaves
// the audit record in the same transaction; a reset of an item already at
// its default changed nothing, so it records nothing.
func (s *Settings) ResetFor(ctx context.Context, callerID, key string) (Setting, error) {
	def, ok := s.byKey[key]
	if !ok {
		return Setting{}, ErrUnknownSetting
	}

	var setting Setting
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		db := sqlbuilder.PostgreSQL.NewDeleteBuilder()
		db.DeleteFrom(SettingTable)
		db.Where(db.Equal("key", key))

		query, args := db.Build()
		tag, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("appconfig: reset setting: %w", err)
		}
		if tag.RowsAffected() == 0 {
			setting = s.atDefault(def)
			return nil
		}

		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventSettingReset,
			Status: audit.StatusSuccess,
			UserID: callerID,
			// The key is not a UUID — the payload carries it.
			Payload: map[string]string{"key": key},
		})
		setting = s.atDefault(def)
		return nil
	})
	if err != nil {
		return Setting{}, err
	}
	s.invalidate(ctx, key)
	return setting, nil
}

// atDefault builds the item as its catalog default, with no override to
// report.
func (s *Settings) atDefault(def SettingDef) Setting {
	return Setting{
		Key:     def.Key,
		Value:   def.Default,
		Default: def.Default,
		Sealed:  def.Sealed,
		Public:  def.Public,
	}
}

// overrides reads the stored rows into a map keyed by the catalog key.
// The publicOnly scope asks only for the keys the public read serves,
// which is a filter the SQL can do because the catalog is code.
func (s *Settings) overrides(ctx context.Context, publicOnly bool) (map[string]SettingSchema, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("key", "value", "created_at", "updated_at")
	sb.From(SettingTable)
	if publicOnly {
		// The catalog is code, so the public keys are a fixed list the SQL
		// can filter on.
		public := make([]any, 0, len(s.defs))
		for _, def := range s.defs {
			if def.Public {
				public = append(public, def.Key)
			}
		}
		sb.Where(sb.In("key", public...))
	}

	query, args := sb.Build()
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("appconfig: list settings: %w", err)
	}
	defer rows.Close()

	overrides := map[string]SettingSchema{}
	for rows.Next() {
		var schema SettingSchema
		if err := rows.Scan(&schema.Key, &schema.Value, &schema.CreatedAt, &schema.UpdatedAt); err != nil {
			return nil, fmt.Errorf("appconfig: list settings: %w", err)
		}
		overrides[schema.Key] = schema
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("appconfig: list settings: %w", err)
	}
	return overrides, nil
}

// fromRow builds the item from a stored override, opening the value when
// the catalog says the item rests sealed.
func (s *Settings) fromRow(def SettingDef, schema SettingSchema) (Setting, error) {
	value, err := s.open(def, schema.Value)
	if err != nil {
		return Setting{}, err
	}
	return Setting{
		Key:       def.Key,
		Value:     value,
		Default:   def.Default,
		Sealed:    def.Sealed,
		Public:    def.Public,
		UpdatedAt: schema.UpdatedAt,
	}, nil
}

// store upserts the override. The value is sealed here, on the way in, when
// the catalog says the item rests sealed — the table never learns the
// plaintext of a sealed item.
func (s *Settings) store(ctx context.Context, q datastore.Querier, key, value string) (Setting, error) {
	def, ok := s.byKey[key]
	if !ok {
		return Setting{}, ErrUnknownSetting
	}
	if !def.Sealed && strings.HasPrefix(value, crypto.EncPrefix) {
		return Setting{}, ErrReservedPrefix
	}

	stored := value
	if def.Sealed {
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
	sb.Cols("key", "value")
	sb.Values(key, stored)
	sb.SQL("ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value")
	sb.Returning("created_at", "updated_at")

	query, args := sb.Build()
	setting := Setting{Key: key, Value: value, Default: def.Default, Sealed: def.Sealed, Public: def.Public}
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
func (s *Settings) open(def SettingDef, stored string) (string, error) {
	if !def.Sealed && !strings.HasPrefix(stored, crypto.EncPrefix) {
		return stored, nil
	}
	if !def.Sealed {
		// A plain item can only carry a prefixed value if something wrote
		// around the feature; the answer fails closed all the same.
		return "", ErrReservedPrefix
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
// so every read path shares the read.
func (s *Settings) scanSetting(def SettingDef, rows pgx.Rows) (Setting, error) {
	var schema SettingSchema
	if err := rows.Scan(&schema.Key, &schema.Value, &schema.CreatedAt, &schema.UpdatedAt); err != nil {
		return Setting{}, fmt.Errorf("appconfig: scan setting: %w", err)
	}
	return s.fromRow(def, schema)
}
