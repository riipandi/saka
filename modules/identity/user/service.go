package user

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"uuid"

	identityv1 "github.com/riipandi/tango/codegen/proto/go/tango/identity/v1"
	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/storage"
	"github.com/riipandi/tango/modules/identity/password"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/responder"
)

// The failures the account procedures report. The handler maps them to
// connect codes, so the wire form of a refusal lives with the transport,
// not here.
var (
	// ErrUserNotFound is a read, update, or delete whose identifier names
	// no account.
	ErrUserNotFound = errors.New("user: account not found")

	// ErrAccountExists covers a taken username and a taken email. The
	// username is matched case-insensitively, the way its unique index is.
	ErrAccountExists = errors.New("user: account already exists")

	// ErrSelfDeletion is the refusal of the one deletion an administrator
	// cannot perform: their own signed-in account.
	ErrSelfDeletion = errors.New("user: cannot delete the signed-in account")

	// ErrTimezoneInvalid is a timezone preference that names no zone the
	// tz database carries. The proto constraint bounds its length; only
	// this check can tell a well-formed name from a real zone.
	ErrTimezoneInvalid = errors.New("user: unknown timezone")

	// ErrUsernameInvalid is a username that carries the shape the column's
	// check enforces — the pattern applies only when the field is present.
	ErrUsernameInvalid = errors.New("user: username is invalid")
)

// ResourceUser is the resource type an audit record names when the account
// itself is what was acted on and the account no longer exists to be named by
// the user column.
const ResourceUser = "user"

// Service administers the accounts.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// audit writes the record of every account change, in the transaction
	// that makes the change.
	audit  *audit.Recorder
	hasher *crypto.PasswordHasher
	log    *slog.Logger
	now    func() time.Time

	// pictures is the storage engine the profile pictures live in. It is
	// nil in the tests that exercise the account procedures only; the
	// picture procedures refuse while it is absent.
	pictures *storage.Manager

	// sessions ends the rows a ban withdraws. It is nil in the tests that
	// exercise the account procedures only; a ban then writes its fields
	// without ending sessions, and the notification interface answers the
	// same question for the mail side.
	sessions sessionEnder

	// notify queues the ban notifications. It is nil where the queue is
	// absent — a ban still writes, only without a message.
	notify banNotifier

	// policy is the credential check's runtime source. Nil keeps the
	// static policy.
	policy *password.Validator

	// settings reads the self-service gates — the username change and the
	// self-delete — at call time. Nil keeps every gate closed: the
	// bare-wiring state answers not-found, the same shape an unknown
	// account earns.
	settings settingsReader

	// groups reads and opens the memberships the account views carry. It
	// is nil where the group feature is not wired — the views then answer
	// without the field filled, which is the smaller feature rather than a
	// broken one.
	groups GroupDirectory
}

// banNotifier queues the messages a ban and its lift produce. It is an
// interface because the delivery is the queue's business: the ban owns what
// happened, the job owns how it reaches the address. A nil notifier is the
// state every test without a queue is in — the ban still writes, only
// silently.
type banNotifier interface {
	// UserBanned queues the ban's notification. The expiry is nil for a ban
	// that never lifts — the message says so rather than naming no date.
	UserBanned(ctx context.Context, email string, subject UserView, expiresAt *time.Time)
	// UserUnbanned queues the lift's notification.
	UserUnbanned(ctx context.Context, email string, subject UserView)
}

// NewService builds the service. The database writes run in one transaction
// the service opens over the pool, so an account and its credential commit
// together or not at all.
func NewService(pool *datastore.Postgres, recorder *audit.Recorder, log *slog.Logger, pictures *storage.Manager) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		pool:     pool,
		repo:     NewRepository(),
		audit:    recorder,
		hasher:   crypto.NewPasswordHasher(),
		log:      log,
		pictures: pictures,
		now:      time.Now,
	}
}

// WithGroups arms the group seam the account views read through. It is
// wired after construction because the group tables belong to the group
// feature — a provider that took the directory would order the area's
// construction around it, and a nil directory degrades the views to
// answering without memberships rather than failing the run.
func (s *Service) WithGroups(directory GroupDirectory) *Service {
	s.groups = directory
	return s
}

// withGroup fills one view's memberships through the group seam. db is the
// query surface the caller already holds — inside a transaction the
// memberships the write just opened are read back through it, so the answer
// never names a join the commit has not made. A nil directory is the state
// a consumer without the group feature is in: the view answers without the
// field filled.
func (s *Service) withGroup(ctx context.Context, db datastore.Querier, filled UserView) (UserView, error) {
	if s.groups == nil {
		return filled, nil
	}
	id, err := parseWire(filled.ID)
	if err != nil {
		return UserView{}, ErrUserNotFound
	}
	groups, err := s.groups.GroupsOfUser(ctx, db, id)
	if err != nil {
		return UserView{}, err
	}
	filled.Groups = groups
	return filled, nil
}

// withGroups fills a page of views in one seam read, the batch the list
// procedure's one answer needs — a per-row query would pay the group read
// once per account on the page.
func (s *Service) withGroups(ctx context.Context, views []UserView) ([]UserView, error) {
	if s.groups == nil || len(views) == 0 {
		return views, nil
	}
	ids := make([]uuid.UUID, 0, len(views))
	for _, filled := range views {
		id, err := parseWire(filled.ID)
		if err != nil {
			return nil, ErrUserNotFound
		}
		ids = append(ids, id)
	}
	byUser, err := s.groups.GroupsOfUsers(ctx, s.pool, ids)
	if err != nil {
		return nil, err
	}
	for i := range views {
		if id, err := parseWire(views[i].ID); err == nil {
			views[i].Groups = byUser[id]
		}
	}
	return views, nil
}

// WithBanSideEffects answers the same service carrying the ban's two side
// effects: the session rows a ban ends and the notifications it queues. Both
// are optional — an absent one degrades the ban to a write — and they are
// wired after construction because the registry resolves the session service
// and the queue beside the user service, not before it.
func (s *Service) WithBanSideEffects(sessions sessionEnder, notify banNotifier) *Service {
	s.sessions = sessions
	s.notify = notify
	return s
}

// WithPasswordPolicy wires the settings-driven validator. Nil keeps the
// static policy — the state a test or a bare wiring is in.
func (s *Service) WithPasswordPolicy(policy *password.Validator) *Service {
	s.policy = policy
	return s
}

// validatePassword runs the credential through the wired policy.
func (s *Service) validatePassword(ctx context.Context, clearText string) error {
	if s.policy != nil {
		return s.policy.Validate(ctx, clearText)
	}
	return password.Validate(clearText)
}

// settingsReader is the self-service gates' runtime source: the toggles read
// fresh at every call, so an operator's change lands without a restart.
// *appconfig.Settings satisfies it; the interface keeps the appconfig
// feature out of this one's import graph.
type settingsReader interface {
	GetBool(ctx context.Context, key string) (bool, error)
}

// The catalog keys the self-service gates read. The catalog owns the names;
// these constants are how this package spells them.
const (
	SettingChangeUsernameEnabled = "users.change_username_enabled"
	SettingSelfDeleteEnabled     = "users.self_delete_enabled"
	SettingChangeEmailEnabled    = "users.change_email_enabled"
)

// WithSettings wires the self-service gates. Nil keeps every gate closed —
// the state a test or a bare wiring is in.
func (s *Service) WithSettings(reader settingsReader) *Service {
	s.settings = reader
	return s
}

// gateOpen answers the self-service gate: the global setting when the
// account carries no override, the override's answer otherwise. An
// unreadable setting refuses — a gate that cannot answer is a gate closed.
func (s *Service) gateOpen(ctx context.Context, key string, override *bool) bool {
	if override != nil {
		return *override
	}
	if s.settings == nil {
		return false
	}
	on, err := s.settings.GetBool(ctx, key)
	if err != nil {
		return false
	}
	return on
}

// usernamePattern mirrors the column's check: 3-32 ASCII letters, digits,
// and underscores. The username is optional by the toggles; present, it
// carries the pattern.
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)

// CreateParams carries one administrator-created account.
type CreateParams struct {
	Username      string
	Email         string
	Password      string // empty means the account carries no credential yet
	FirstName     string
	LastName      string
	DisplayName   string
	Locale        string
	Disabled      bool
	EmailVerified bool
	// GroupIDs are the memberships the account opens with, in the wire
	// form the request carried. Empty creates a memberless account.
	GroupIDs []string
}

// UpdateParams carries the replacement fields of one account. The ban fields
// are one unit: BanExpiresAt present applies the ban — the start instant is
// recorded here, keeping an earlier one — absent, it lifts it.
type UpdateParams struct {
	ID           uuid.UUID
	Username     string
	Email        string
	FirstName    string
	LastName     string
	DisplayName  string
	Locale       string
	Timezone     string
	Disabled     bool
	BanExpiresAt *time.Time
	BanReason    *string
}

// ProfileParams carries the fields a signed-in account may change about
// itself: the display surface, and the username where the change toggle
// admits it. The narrow shape is the self-service boundary — a caller
// cannot smuggle a role or an address through a request the administrative
// surface does not own. An empty username keeps the current one; the
// username is optional by the toggles, so there is no "clear" here.
type ProfileParams struct {
	Username    string
	FirstName   string
	LastName    string
	DisplayName string
	Locale      string
	Timezone    string
}

// DefaultTimezone is the preference every account starts with and an empty
// presented value resolves to. Timestamps leave the server in UTC regardless;
// this is the hint the frontend formats them against.
//
// TODO(frontend): the SPA has no profile surface yet. When one lands, it
// should render `User.timezone` over the browser's own detection — offer a
// picker of IANA zones, defaulting to UTC — and format every instant the
// API answers in that zone.
const DefaultTimezone = "UTC"

// normalizeTimezone validates a presented timezone preference. An empty value
// is the default — clearing the field means UTC, not an unparseable zone —
// and `Local` is refused because a client-side preference that means
// "wherever this server runs" is never what its presenter intended. A
// non-empty value must name a zone the tz database knows: the check runs at
// the boundary, so no row can hold a name the frontend cannot load.
func normalizeTimezone(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultTimezone, nil
	}
	if value == "Local" {
		return "", ErrTimezoneInvalid
	}
	if _, err := time.LoadLocation(value); err != nil {
		return "", ErrTimezoneInvalid
	}
	return value, nil
}

// UserView is an account as the procedures answer it: the fields a client
// renders or an operator manages, never the credential hash.
type UserView struct {
	ID          string
	Username    string
	Email       string
	DisplayName string
	FirstName   *string
	LastName    *string
	Locale      *string
	Timezone    string
	// Groups are the memberships the account answers with, ordered by the
	// group's display name. Empty when the group seam is not wired or the
	// account belongs to none.
	Groups        []GroupSummary
	Disabled      bool
	EmailVerified bool
	CreatedAt     time.Time
	BannedAt      *time.Time
	BanExpires    *time.Time
	BanReason     *string
}

// CreateUser creates an account directly, without a signup token. The
// optional password is hashed and stored with the account; absent, the
// account carries no credential until a later procedure sets one.
func (s *Service) CreateUser(ctx context.Context, params CreateParams) (UserView, error) {
	passwordHash := ""
	if params.Password != "" {
		if validateErr := s.validatePassword(ctx, params.Password); validateErr != nil {
			return UserView{}, validateErr
		}
		hash, err := s.hasher.Hash(params.Password)
		if err != nil {
			return UserView{}, fmt.Errorf("user: hash password: %w", err)
		}
		passwordHash = hash
	}

	displayName := params.DisplayName
	if displayName == "" {
		displayName = DisplayName(params.FirstName, params.LastName)
	}

	var emailVerifiedAt *time.Time
	if params.EmailVerified {
		at := s.now()
		emailVerifiedAt = &at
	}

	var created UserView
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		id, createErr := s.repo.CreateUser(ctx, tx, UserSchema{
			Username:        params.Username,
			Email:           params.Email,
			FirstName:       params.FirstName,
			LastName:        params.LastName,
			DisplayName:     displayName,
			Locale:          params.Locale,
			Timezone:        DefaultTimezone,
			Disabled:        params.Disabled,
			EmailVerifiedAt: emailVerifiedAt,
			CreatedAt:       s.now(),
		})
		if errUniqueViolation(createErr) {
			return ErrAccountExists
		}
		if createErr != nil {
			return fmt.Errorf("user: create: %w", createErr)
		}

		if passwordHash != "" {
			if passErr := s.repo.CreatePassword(ctx, tx, id, passwordHash); passErr != nil {
				return passErr
			}
		}

		// The database fills the columns the insert omits (the creation
		// instant among them), so the view is read back rather than
		// assembled from the request.
		row, readErr := s.repo.GetUser(ctx, tx, id)
		if readErr != nil {
			return readErr
		}

		// The memberships open in the account's transaction: a group id
		// the directory cannot name rolls the whole creation back, so an
		// account is never created with a membership half-applied.
		if len(params.GroupIDs) > 0 {
			if attachErr := s.groups.AttachGroups(ctx, tx, id, params.GroupIDs); attachErr != nil {
				return attachErr
			}
		}

		created = view(row)
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventAccountCreated,
			Status: audit.StatusSuccess,
			UserID: id.String(),
			Payload: map[string]string{
				"username": row.Username,
				"email":    row.Email,
				"source":   "admin",
			},
		})
		return nil
	})
	if err != nil {
		return UserView{}, err
	}
	return s.withGroup(ctx, s.pool, created)
}

// GetUser answers one account by its identifier.
func (s *Service) GetUser(ctx context.Context, id string) (UserView, error) {
	userID, err := parseWire(id)
	if err != nil {
		return UserView{}, ErrUserNotFound
	}
	row, err := s.repo.GetUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return UserView{}, ErrUserNotFound
	}
	if err != nil {
		return UserView{}, err
	}
	filled, err := s.withGroup(ctx, s.pool, view(row))
	if err != nil {
		return UserView{}, err
	}
	return filled, nil
}

// GetCurrentUser answers the account the caller is. The caller's identifier
// is the token's subject — a wire-form TypeID — so the read takes no target
// from the request. An account the identifier no longer names is the
// not-found failure, the same refusal a deleted account earns anywhere.
func (s *Service) GetCurrentUser(ctx context.Context, subject string) (UserView, error) {
	userID, err := parseWire(subject)
	if err != nil {
		return UserView{}, ErrUserNotFound
	}
	row, err := s.repo.GetUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return UserView{}, ErrUserNotFound
	}
	if err != nil {
		return UserView{}, err
	}
	filled, err := s.withGroup(ctx, s.pool, view(row))
	if err != nil {
		return UserView{}, err
	}
	return filled, nil
}

// UpdateCurrentUser replaces the signed-in account's own profile fields.
// The writable set is deliberately narrower than the administrative
// replace: the names and the locale travel, the credential and the role do
// not. The self rule the guard applied has already established the caller
// is the account; the read supplies the immutable columns the update
// statement leaves alone.
func (s *Service) UpdateCurrentUser(ctx context.Context, subject string, params ProfileParams) (UserView, error) {
	userID, err := parseWire(subject)
	if err != nil {
		return UserView{}, ErrUserNotFound
	}
	existing, err := s.repo.GetUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return UserView{}, ErrUserNotFound
	}
	if err != nil {
		return UserView{}, err
	}

	// The username is the gated field: absent keeps the current one, and a
	// present value goes through the toggle, the pattern, and the column's
	// unique index — in that order, so a refused change is a cheap refusal.
	username := existing.Username
	usernameChanged := false
	if params.Username != "" && !strings.EqualFold(params.Username, existing.Username) {
		if !s.gateOpen(ctx, SettingChangeUsernameEnabled, nil) {
			return UserView{}, ErrUserNotFound
		}
		if !usernamePattern.MatchString(params.Username) {
			return UserView{}, ErrUsernameInvalid
		}
		username = params.Username
		usernameChanged = true
	}

	timezone, tzErr := normalizeTimezone(params.Timezone)
	if tzErr != nil {
		return UserView{}, tzErr
	}

	row := UserSchema{
		ID:          userID,
		Username:    username,
		Email:       existing.Email,
		FirstName:   params.FirstName,
		LastName:    params.LastName,
		DisplayName: params.DisplayName,
		Locale:      params.Locale,
		Timezone:    timezone,
		Disabled:    existing.Disabled,
		// The immutable columns and the ban state ride through untouched:
		// this update owns the profile, nothing else.
		CreatedAt:       existing.CreatedAt,
		BannedAt:        existing.BannedAt,
		BanExpires:      existing.BanExpires,
		BanReason:       existing.BanReason,
		EmailVerifiedAt: existing.EmailVerifiedAt,
	}

	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		updated, updateErr := s.repo.UpdateUser(ctx, tx, row)
		if errUniqueViolation(updateErr) {
			return ErrAccountExists
		}
		if updateErr != nil {
			return updateErr
		}
		if !updated {
			return ErrUserNotFound
		}
		// A username change is its own happening: the profile update's
		// record says the row moved, this one says the handle did — the
		// old name is the payload, the new one the row.
		if usernameChanged {
			s.audit.Record(ctx, tx, audit.Entry{
				Event:  audit.EventUsernameChanged,
				Status: audit.StatusSuccess,
				UserID: userID.String(),
				Payload: map[string]string{
					"username":     username,
					"old_username": existing.Username,
					"source":       "self",
				},
			})
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventAccountUpdated,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"username": row.Username,
				"source":   "self",
			},
		})
		return nil
	})
	if err != nil {
		return UserView{}, err
	}
	filled, err := s.withGroup(ctx, s.pool, view(row))
	if err != nil {
		return UserView{}, err
	}
	return filled, nil
}

// DeleteMyAccount removes the signed-in account itself. The gate is the
// global setting with the account's override answering for it — and a gate
// that refuses, or a subject that names no account, earns the same
// not-found-shaped answer: the surface says nothing about what the setting
// holds. The removal is the administrator delete's own mechanics — a real
// DELETE the database's soft-delete trigger archives, the dependent rows
// resolved by their own table rules — recorded inside the same transaction,
// naming the account in resource_type and resource_id because the row is
// gone by the time the record is written.
func (s *Service) DeleteMyAccount(ctx context.Context, subject string) error {
	userID, err := parseWire(subject)
	if err != nil {
		return ErrUserNotFound
	}
	row, err := s.repo.GetUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if !s.gateOpen(ctx, SettingSelfDeleteEnabled, row.SelfDeleteOverride) {
		return ErrUserNotFound
	}

	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		deleted, deleteErr := s.repo.DeleteUser(ctx, tx, userID)
		if deleteErr != nil {
			return deleteErr
		}
		if !deleted {
			return ErrUserNotFound
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventAccountDeleted,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceUser,
			ResourceID:   userID.String(),
			Payload: map[string]string{
				"username": row.Username,
				"email":    row.Email,
				"source":   "self",
			},
		})
		return nil
	})
}

// ListUsers answers one page of the accounts, newest first, optionally
// filtered by a search term.
func (s *Service) ListUsers(ctx context.Context, search, sortBy string, ascending bool, page, limit int) ([]UserView, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)

	rows, total, err := s.repo.ListUsers(ctx, s.pool, search, sortBy, ascending, responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
	}

	views := make([]UserView, 0, len(rows))
	for _, row := range rows {
		views = append(views, view(row))
	}
	filled, err := s.withGroups(ctx, views)
	if err != nil {
		return nil, responder.Pagination{}, err
	}
	return filled, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
}

// UpdateUser replaces an account's writable fields. The account is read
// first: it is the not-found check, and it supplies the ban's start instant
// the automatic recording keeps.
func (s *Service) UpdateUser(ctx context.Context, id string, params UpdateParams) (UserView, error) {
	userID, err := parseWire(id)
	if err != nil {
		return UserView{}, ErrUserNotFound
	}
	existing, err := s.repo.GetUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return UserView{}, ErrUserNotFound
	}
	if err != nil {
		return UserView{}, err
	}

	timezone, tzErr := normalizeTimezone(params.Timezone)
	if tzErr != nil {
		return UserView{}, tzErr
	}

	row := UserSchema{
		ID:          userID,
		Username:    params.Username,
		Email:       params.Email,
		FirstName:   params.FirstName,
		LastName:    params.LastName,
		DisplayName: params.DisplayName,
		Locale:      params.Locale,
		Timezone:    timezone,
		Disabled:    params.Disabled,
		// The creation instant is immutable; the update statement leaves the
		// column alone, and the answer carries the value as it stood.
		CreatedAt:  existing.CreatedAt,
		BannedAt:   existing.BannedAt,
		BanExpires: params.BanExpiresAt,
		BanReason:  params.BanReason,
	}
	if params.BanExpiresAt != nil {
		// The ban is applied now unless an earlier one is on record: the
		// start instant answers "since when", so a re-ban does not move it.
		if existing.BannedAt == nil {
			at := s.now()
			row.BannedAt = &at
		}
	} else {
		// Absent expiry lifts the ban as a unit, reason included.
		row.BannedAt = nil
		row.BanReason = nil
	}

	// The update and its record commit together, so the log cannot name a
	// change the database refused.
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		updated, updateErr := s.repo.UpdateUser(ctx, tx, row)
		if errUniqueViolation(updateErr) {
			return ErrAccountExists
		}
		if updateErr != nil {
			return updateErr
		}
		if !updated {
			return ErrUserNotFound
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventAccountUpdated,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"username": row.Username,
				"email":    row.Email,
			},
		})
		return nil
	})
	if err != nil {
		return UserView{}, err
	}
	filled, err := s.withGroup(ctx, s.pool, view(row))
	if err != nil {
		return UserView{}, err
	}
	return filled, nil
}

// ReadAccount reads one account row and answers the canonical view. It is
// the read-back the account-creating features share: sign-up assembles its
// answer from it, so both doors describe the account the same way.
func ReadAccount(ctx context.Context, db datastore.Querier, id uuid.UUID) (UserView, error) {
	row, err := (&Repository{}).GetUser(ctx, db, id)
	if err != nil {
		return UserView{}, err
	}
	return view(row), nil
}

// ViewSchema maps a stored row onto the account view, for the features that
// answer accounts they read through this package: the row is this package's
// shape, the view is what every procedure answers with.
func ViewSchema(row UserSchema) UserView {
	return view(row)
}

// WireView maps the account view onto the wire message the identity
// contract carries. The procedures that answer an account — sign-up and the
// administration CRUD — share it, so the wire form of an account is written
// once. The optional wire fields carry the NULLs, so an absent locale or
// ban reads as absent rather than as an empty string.
func WireView(user UserView) *identityv1.User {
	view := &identityv1.User{
		Id:            user.ID,
		Username:      user.Username,
		Email:         user.Email,
		DisplayName:   user.DisplayName,
		FirstName:     user.FirstName,
		LastName:      user.LastName,
		Locale:        user.Locale,
		Timezone:      user.Timezone,
		Disabled:      user.Disabled,
		EmailVerified: user.EmailVerified,
		CreatedAt:     user.CreatedAt.Format(rfc3339),
		BanReason:     user.BanReason,
	}
	if user.BannedAt != nil {
		view.BannedAt = new(user.BannedAt.Format(rfc3339))
	}
	if user.BanExpires != nil {
		view.BanExpires = new(user.BanExpires.Format(rfc3339))
	}
	for _, group := range user.Groups {
		wire := &identityv1.UserGroup{
			Id:          group.ID,
			Name:        group.Name,
			DisplayName: group.DisplayName,
			UserCount:   group.UserCount,
			CreatedAt:   group.CreatedAt.Format(rfc3339),
		}
		if group.UpdatedAt != nil {
			wire.UpdatedAt = new(group.UpdatedAt.Format(rfc3339))
		}
		view.UserGroups = append(view.UserGroups, wire)
	}
	return view
}

// rfc3339 is the timestamp form the wire views carry.
const rfc3339 = "2006-01-02T15:04:05Z07:00"

// DeleteUser removes an account. The caller's username travels with the
// request, because the one deletion an administrator cannot perform is the
// account they are signed in with — the identifier is compared
// case-insensitively, the way the username column matches.
func (s *Service) DeleteUser(ctx context.Context, id, callerUsername string) error {
	userID, err := parseWire(id)
	if err != nil {
		return ErrUserNotFound
	}
	row, err := s.repo.GetUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if strings.EqualFold(row.Username, callerUsername) {
		return ErrSelfDeletion
	}

	// The record of a deletion cannot name its account through the user
	// column: the row is gone by the time the record is written, and the
	// foreign key — `ON DELETE SET NULL`, so a record outlives its account —
	// refuses an identifier that names nothing. The account is named the way
	// any other acted-on resource is, in resource_type and resource_id, and
	// the payload keeps the username so a reader can still tell whose
	// deletion this was.
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		deleted, deleteErr := s.repo.DeleteUser(ctx, tx, userID)
		if deleteErr != nil {
			return deleteErr
		}
		if !deleted {
			return ErrUserNotFound
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventAccountDeleted,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceUser,
			ResourceID:   userID.String(),
			Payload: map[string]string{
				"username": row.Username,
				"email":    row.Email,
			},
		})
		return nil
	})
}

// view maps a stored row onto the account view. The identifier is the wire
// form: a TypeID the responses and the URLs carry, so the row's UUID stays
// inside the server.
func view(row UserSchema) UserView {
	return UserView{
		ID:            FormatID(row.ID),
		Username:      row.Username,
		Email:         row.Email,
		DisplayName:   row.DisplayName,
		FirstName:     optional(row.FirstName),
		LastName:      optional(row.LastName),
		Locale:        optional(row.Locale),
		Timezone:      row.Timezone,
		Disabled:      row.Disabled,
		EmailVerified: row.EmailVerifiedAt != nil,
		CreatedAt:     row.CreatedAt,
		BannedAt:      row.BannedAt,
		BanExpires:    row.BanExpires,
		BanReason:     row.BanReason,
	}
}

// optional hands the view a pointer for a column that may be NULL.
func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// DisplayName composes the name the UI shows from the given and family
// names. Both are mandatory, so the composition is always a name; the
// account-creating features share it, so a sign-up and an administrator
// creation name an account the same way.
func DisplayName(firstName, lastName string) string {
	return strings.TrimSpace(strings.Join([]string{strings.TrimSpace(firstName), strings.TrimSpace(lastName)}, " "))
}

// parseWire turns the request's identifier into the key the rows carry. The
// wire form is the TypeID the responses and the URLs speak; an identifier
// without the prefix names no account, the same refusal an unknown one earns.
func parseWire(id string) (uuid.UUID, error) {
	wire, err := ParseID(id)
	if err != nil {
		return uuid.Nil(), err
	}
	return IDToUUID(wire), nil
}
