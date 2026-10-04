package seeders

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"
	"github.com/jackc/pgx/v5"

	"github.com/riipandi/saka/internal/authz"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/identity/password"
	"github.com/riipandi/saka/modules/identity/restrictions"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/crypto"
)

// UserSeederName is the name this seeder reports under. The "Seeder" suffix
// keeps it distinct from the entity it writes, which appears next to it on the
// same report line.
const UserSeederName = "UserSeeder"

// UserCredentials is the account the default user is created with. The values
// are public on purpose: they bootstrap a local database, and a deployment is
// expected to change the password at first login.
type UserCredentials struct {
	Email     string
	Username  string
	Password  string
	FirstName string
	LastName  string
}

// DisplayName is the name shown in the UI. It is derived rather than stored, so
// the credentials stay the single source and the column cannot disagree with
// the names it is built from. It is trimmed because display_name rejects an
// empty string, and an account may carry only one of the two names.
func (c UserCredentials) DisplayName() string {
	return strings.TrimSpace(c.FirstName + " " + c.LastName)
}

// DefaultUser is the account migrate:seed creates.
var DefaultUser = UserCredentials{
	Email:     "admin@example.com",
	Username:  "admin",
	Password:  "@dmin123",
	FirstName: "Admin",
	LastName:  "Sistem",
}

// Scenario accounts the seeder writes beside the default one, one per ban
// state the surface can answer, so a local database can exercise every path
// without hand-writing rows. All of them share the default password.
var scenarioUsers = []scenarioUser{
	{
		credentials: UserCredentials{
			Email:     "robert.langdon@example.com",
			Username:  "robert_langdon",
			Password:  "@dmin123",
			FirstName: "Robert",
			LastName:  "Langdon",
		},
	},
	{
		credentials: UserCredentials{
			Email:     "sophie.neveu@example.com",
			Username:  "sophie_neveu",
			Password:  "@dmin123",
			FirstName: "Sophie",
			LastName:  "Neveu",
		},
	},
	{
		// A permanent ban: no expiry, a stated reason. Sign-in and refresh
		// refuse the account, and the notification names no end date.
		credentials: UserCredentials{
			Email:     "silas.vetra@example.com",
			Username:  "silas_vetra",
			Password:  "@dmin123",
			FirstName: "Silas",
			LastName:  "Vetra",
		},
		bannedAt:  true,
		banReason: "Repeated violations of the community guidelines",
	},
	{
		// A ban still inside its window: refused now, lifting by itself.
		credentials: UserCredentials{
			Email:     "hermione.granger@example.com",
			Username:  "hermione_granger",
			Password:  "@dmin123",
			FirstName: "Hermione",
			LastName:  "Granger",
		},
		bannedAt:   true,
		banExpires: true,
		banReason:  "Awaiting the moderation review",
	},
	{
		// A ban whose window has passed: the row keeps the history, and the
		// account can sign in again — the expiry is the lift.
		credentials: UserCredentials{
			Email:     "vittoria.vetra@example.com",
			Username:  "vittoria_vetra",
			Password:  "@dmin123",
			FirstName: "Vittoria",
			LastName:  "Vetra",
		},
		bannedAt:   true,
		banExpires: true,
		banExpired: true,
		banReason:  "Outgrown suspension",
	},
}

// scenarioUser is one account the seeder writes beside the default one: the
// credentials it signs in with, and the ban state the scenario exercises.
type scenarioUser struct {
	credentials UserCredentials
	bannedAt    bool
	// banExpires writes a window; banExpired moves it into the past, so the
	// row answers "banned once, free now" instead of "banned still".
	banExpires bool
	banExpired bool
	banReason  string
}

// ScenarioEmails are the addresses the scenario accounts sign in with —
// the list a test counts against when it asserts the seeder's full output.
var ScenarioEmails = []string{
	"robert.langdon@example.com",
	"sophie.neveu@example.com",
	"silas.vetra@example.com",
	"hermione.granger@example.com",
	"vittoria.vetra@example.com",
}

// User returns the seeder for the default user and the scenario accounts. It
// writes each identity and its password in one transaction, because a user
// without a password cannot log in and the two tables are one record
// conceptually.
func User() Seeder {
	return Seeder{
		Name:  UserSeederName,
		Apply: applyDefaultUser,
	}
}

// applyDefaultUser creates the default user and its password, then the
// scenario accounts — one per ban state the surface answers.
//
// Every insert is guarded by ON CONFLICT DO NOTHING, so a second run keeps
// the existing rows and reports them as skipped. Accounts are looked up by
// email, the natural key the operator knows.
func applyDefaultUser(
	ctx context.Context,
	q datastore.Querier,
	dryRun bool,
) (created, skipped []string, err error) {
	if dryRun {
		return plannedDefaultUser(ctx, q)
	}

	// The password hash is salted at random, so it must not be computed on a
	// dry run: that work would be thrown away. The scenarios share it — one
	// hash, many rows, the salt protecting each row on its own.
	hash, err := crypto.NewPasswordHasher().Hash(DefaultUser.Password)
	if err != nil {
		return nil, nil, err
	}

	// Seeded accounts ship verified: the sign-in gate the verification
	// setting arms refuses an unverified address, and a bootstrap account
	// the operator cannot receive mail for must not be locked out by it.
	now := time.Now().UTC()
	row := user.UserSchema{
		ID:              uuid.NewV7(),
		Username:        DefaultUser.Username,
		Email:           DefaultUser.Email,
		FirstName:       DefaultUser.FirstName,
		LastName:        DefaultUser.LastName,
		DisplayName:     DefaultUser.DisplayName(),
		CreatedAt:       time.Now().UTC(),
		EmailVerifiedAt: &now,
	}

	inserted, err := insertUser(ctx, q, row)
	if err != nil {
		return nil, nil, err
	}
	if !inserted {
		skipped = append(skipped, DefaultUser.Email)
	} else {
		if err := insertPassword(ctx, q, row.ID, hash); err != nil {
			return nil, nil, err
		}
		created = append(created, DefaultUser.Email)
	}

	// The default account carries the administrator role: it is the
	// bootstrap identity, and with is_admin gone the role is the only thing
	// that makes it an administrator. The grant is idempotent like the
	// account — an active grant exists once, a second run keeps it.
	granted, grantErr := ensureRoleGrant(ctx, q, DefaultUser.Email, authz.AdministratorRole, dryRun)
	if grantErr != nil {
		return nil, nil, grantErr
	}
	if granted {
		created = append(created, DefaultUser.Email+" ("+authz.AdministratorRole+" role)")
	}

	for i := range scenarioUsers {
		email, inserted, scenarioErr := applyScenarioUser(ctx, q, scenarioUsers[i], hash)
		if scenarioErr != nil {
			return nil, nil, scenarioErr
		}
		if inserted {
			created = append(created, email)
		} else {
			skipped = append(skipped, email)
		}
	}

	// The scenario roles ride the accounts that exercise them: the editor
	// reads what the editors group sees, the moderator answers for the
	// moderation surface. A token minted for either carries both roles in
	// its claims, which is the shape a multi-role client must render.
	scenarioGrants := []struct {
		email string
		role  string
	}{
		{ScenarioEmails[0], "editor"},
		{ScenarioEmails[3], "moderator"},
	}
	for _, grant := range scenarioGrants {
		granted, grantErr := ensureRoleGrant(ctx, q, grant.email, grant.role, dryRun)
		if grantErr != nil {
			return nil, nil, grantErr
		}
		if granted {
			created = append(created, grant.email+" ("+grant.role+" role)")
		}
	}

	// One direct permission grant closes the last path the claims carry:
	// the union of a role's set and the account's own grants. Sophie's
	// notice-writing power comes from no role — it is hers alone, the
	// exception the direct grant table exists for.
	directGrants := []struct {
		email string
		slug  string
	}{
		{ScenarioEmails[1], "notification:*:create"},
	}
	for _, grant := range directGrants {
		granted, grantErr := ensurePermissionGrant(ctx, q, grant.email, grant.slug, dryRun)
		if grantErr != nil {
			return nil, nil, grantErr
		}
		if granted {
			created = append(created, grant.email+" ("+grant.slug+" grant)")
		}
	}
	return created, skipped, nil
}

// applyScenarioUser writes one scenario account and answers its email plus
// whether this run created it. The ban state rides as its own restriction
// row — the ban's storage is public.account_restrictions, not a users
// column — so a scenario is one account write plus the row its state needs.
func applyScenarioUser(ctx context.Context, q datastore.Querier, s scenarioUser, hash string) (string, bool, error) {
	now := time.Now().UTC()
	row := user.UserSchema{
		ID:          uuid.NewV7(),
		Username:    s.credentials.Username,
		Email:       s.credentials.Email,
		FirstName:   s.credentials.FirstName,
		LastName:    s.credentials.LastName,
		DisplayName: s.credentials.DisplayName(),
		CreatedAt:   now,
		// Scenario accounts ship verified, like the default one: they exist
		// to exercise the ban states, not the verification gate.
		EmailVerifiedAt: &now,
	}

	inserted, err := insertUser(ctx, q, row)
	if err != nil {
		return "", false, err
	}
	if !inserted {
		return s.credentials.Email, false, nil
	}
	if err := insertPassword(ctx, q, row.ID, hash); err != nil {
		return "", false, err
	}
	if s.bannedAt {
		if err := insertBan(ctx, q, row.ID, s.banReason, s.banExpires, s.banExpired, now); err != nil {
			return "", false, err
		}
	}
	return s.credentials.Email, true, nil
}

// insertBan writes the scenario's ban restriction. The start instant sits a
// day back, so the two expiry scenarios read honestly: one window is open
// (25h from a day ago, an hour ahead), one has passed (23h from a day ago,
// an hour back).
func insertBan(ctx context.Context, q datastore.Querier, userID uuid.UUID, reason string, withExpiry, expired bool, now time.Time) error {
	var expiresAt *time.Time
	if withExpiry {
		at := now.Add(-24 * time.Hour).Add(25 * time.Hour)
		if expired {
			at = now.Add(-24 * time.Hour).Add(23 * time.Hour)
		}
		expiresAt = &at
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableAccountRestrictions)
	ib.Cols("user_id", "kind", "reason", "started_at", "expires_at")
	ib.Values(userID, restrictions.KindBan, reason, now.Add(-24*time.Hour), expiresAt)

	query, args := ib.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("seed ban restriction: %w", err)
	}
	return nil
}

// plannedDefaultUser reports what a real run would do, without writing
// anything and without hashing the password.
func plannedDefaultUser(
	ctx context.Context,
	q datastore.Querier,
) (created, skipped []string, err error) {
	for _, email := range append([]string{DefaultUser.Email}, ScenarioEmails...) {
		exists, existsErr := userExists(ctx, q, email)
		if existsErr != nil {
			return nil, nil, existsErr
		}
		if exists {
			skipped = append(skipped, email)
		} else {
			created = append(created, email)
		}
	}
	return created, skipped, nil
}

// userExists reports whether an account already uses email.
//
// The comparison is exact. public.users.email is TEXT with a format CHECK, not
// citext — only username is case-insensitive — so two accounts may differ by
// case. The seeder matches what the database does: it claims its own address
// and leaves a differently-cased one alone, which is what the unique index on
// email would do anyway.
func userExists(ctx context.Context, q datastore.Querier, email string) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("id").From(entity.TableUsers).Where(sb.Equal("email", email))

	query, args := sb.Build()
	var id uuid.UUID
	err := q.QueryRow(ctx, query, args...).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ensureRoleGrant grants the role the slug names to the account the email
// names, unless an active grant already carries it. It answers whether this
// run wrote the grant. The role row must exist — the authorization seeder
// runs first — and the account row must exist — the user seeder created it
// a moment ago — but either miss is a skip, not a failure: a standalone run
// of this seeder (a test, a rollback probe) carries neither row.
func ensureRoleGrant(ctx context.Context, q datastore.Querier, email, roleSlug string, dryRun bool) (bool, error) {
	if dryRun {
		return false, nil
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("u.id")
	sb.From(entity.TableUsers + " u")
	sb.Where(sb.Equal("u.email", email))

	query, args := sb.Build()
	var userID string
	if err := q.QueryRow(ctx, query, args...).Scan(&userID); err != nil {
		return false, nil
	}

	rb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	rb.Select("r.id")
	rb.From(entity.TableRoles + " r")
	rb.Where(rb.Equal("r.slug", roleSlug))

	query, args = rb.Build()
	var roleID string
	if err := q.QueryRow(ctx, query, args...).Scan(&roleID); err != nil {
		return false, nil
	}

	active := sqlbuilder.PostgreSQL.NewSelectBuilder()
	active.Select("1")
	active.From(entity.TableUserRoles)
	active.Where(active.Equal("user_id", userID), active.Equal("role_id", roleID), active.IsNull("revoked_at"))

	query, args = active.Build()
	var one int
	if q.QueryRow(ctx, query, args...).Scan(&one) == nil {
		return false, nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUserRoles)
	ib.Cols("user_id", "role_id")
	ib.Values(userID, roleID)

	query, args = ib.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return false, fmt.Errorf("grant %s role: %w", roleSlug, err)
	}
	return true, nil
}

// ensurePermissionGrant grants the permission the slug names directly to the
// account the email names, unless an active grant already carries it. It
// answers whether this run wrote the grant. A missing account or permission
// row is a skip, not a failure — a standalone run of this seeder carries
// neither row, and the authorization seeder owns the catalog.
func ensurePermissionGrant(ctx context.Context, q datastore.Querier, email, slug string, dryRun bool) (bool, error) {
	if dryRun {
		return false, nil
	}

	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("u.id")
	sb.From(entity.TableUsers + " u")
	sb.Where(sb.Equal("u.email", email))

	query, args := sb.Build()
	var userID string
	if err := q.QueryRow(ctx, query, args...).Scan(&userID); err != nil {
		return false, nil
	}

	pb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	pb.Select("p.id")
	pb.From(entity.TablePermissions + " p")
	pb.Where(pb.Equal("p.slug", slug))

	query, args = pb.Build()
	var permissionID string
	if err := q.QueryRow(ctx, query, args...).Scan(&permissionID); err != nil {
		return false, nil
	}

	active := sqlbuilder.PostgreSQL.NewSelectBuilder()
	active.Select("1")
	active.From(entity.TableUserPermissions)
	active.Where(active.Equal("user_id", userID), active.Equal("permission_id", permissionID), active.IsNull("revoked_at"))

	query, args = active.Build()
	var one int
	if q.QueryRow(ctx, query, args...).Scan(&one) == nil {
		return false, nil
	}

	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUserPermissions)
	ib.Cols("user_id", "permission_id")
	ib.Values(userID, permissionID)

	query, args = ib.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return false, fmt.Errorf("grant %s permission: %w", slug, err)
	}
	return true, nil
}

// insertUser inserts the account and reports whether it was this run that
// created it. A conflict on any unique column leaves the existing row alone and
// returns no row, which is what makes a repeated seed safe. The columns are
// explicit — the schema struct carries the ban read model's tags, and a
// generated insert would name columns no table has.
func insertUser(ctx context.Context, q datastore.Querier, row user.UserSchema) (bool, error) {
	ib := sqlbuilder.PostgreSQL.NewInsertBuilder()
	ib.InsertInto(entity.TableUsers)
	ib.Cols("id", "username", "email", "first_name", "last_name", "display_name",
		"metadata", "disabled", "email_verified_at", "created_at")
	ib.Values(row.ID, row.Username, row.Email, row.FirstName, row.LastName, row.DisplayName,
		row.Metadata, row.Disabled, row.EmailVerifiedAt, row.CreatedAt).
		SQL("ON CONFLICT DO NOTHING").
		Returning("id")

	query, args := ib.Build()
	var id uuid.UUID
	err := q.QueryRow(ctx, query, args...).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// insertPassword stores the password hash for a user this run created. There is
// no conflict to expect — the account is new — so a conflict is a real error
// rather than something to skip.
func insertPassword(ctx context.Context, q datastore.Querier, userID uuid.UUID, hash string) error {
	ib := sqlbuilder.NewStruct(password.UserPasswordSchema{}).For(sqlbuilder.PostgreSQL).
		InsertInto(entity.TableUserPasswords, password.UserPasswordSchema{
			UserID:       userID,
			PasswordHash: hash,
		})

	query, args := ib.Build()
	if _, err := q.Exec(ctx, query, args...); err != nil {
		return err
	}
	return nil
}
