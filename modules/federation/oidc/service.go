package oidc

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/storage"
	"github.com/riipandi/tango/modules/identity/usergroup"
	"github.com/riipandi/tango/pkg/responder"
)

// Service carries the rules of the OIDC clients: how a client is defined,
// how its secrets are drawn and shown once, and what a change records. The
// repository carries the SQL; secrets.go, logo.go, and preview.go carry the
// concerns beside the lifecycle.
type Service struct {
	pool *datastore.Postgres
	repo *Repository
	// audit writes the record of every client change, in the transaction
	// that makes the change.
	audit *audit.Recorder
	log   *slog.Logger
	now   func() time.Time

	// users is the account facts the preview reads. It is nil in the tests
	// that exercise the management procedures only; the preview refuses
	// while it is absent.
	users UserDirectory

	// pictures is the storage engine the logos live in. It is nil in the
	// tests that exercise the management procedures only; the logo
	// procedures refuse while it is absent.
	pictures *storage.Manager

	// baseURL is the origin the logo URLs name. An empty one renders a
	// relative URL, which a browser resolves against the host it reached.
	baseURL string
}

// NewService builds the service. The database writes run in one transaction
// the service opens over the pool, so a client and its audit record commit
// together or not at all.
func NewService(pool *datastore.Postgres, recorder *audit.Recorder, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		pool:  pool,
		repo:  NewRepository(),
		audit: recorder,
		log:   log,
		now:   time.Now,
	}
}

// WithUserDirectory wires the account facts the preview reads.
func (s *Service) WithUserDirectory(users UserDirectory) *Service {
	s.users = users
	return s
}

// WithPictures wires the storage engine the logos live in.
func (s *Service) WithPictures(pictures *storage.Manager) *Service {
	s.pictures = pictures
	return s
}

// WithBaseURL sets the origin the logo URLs name.
func (s *Service) WithBaseURL(baseURL string) *Service {
	s.baseURL = baseURL
	return s
}

// CreateParams is a creation's fields. The identifier is optional — the
// protocol credential a foreign client presents is generated when the
// operator supplies none.
type CreateParams struct {
	ID                                  string
	Name                                string
	Description                         string
	CallbackURLs                        []string
	LogoutCallbackURLs                  []string
	LaunchURL                           *string
	IsPublic                            bool
	PkceEnabled                         bool
	RequiresReauthentication            bool
	RequiresPushedAuthorizationRequests bool
	SkipConsent                         bool
	AccessTokenDurationMinutes          int64
	RefreshTokenDurationMinutes         int64
	AllowedGroupWires                   []string
}

// Issued is what a creation answers: the client's view and the raw first
// secret, which exists in this response alone.
type Issued struct {
	Client ClientView
	Secret string
}

// UpdateParams is a rewrite's fields. The full replace is the shape the
// upstream update keeps: a field the request leaves out falls back to
// empty, and the secrets, the logo, and the restriction are not fields of
// this call.
type UpdateParams struct {
	Name                                string
	Description                         string
	CallbackURLs                        []string
	LogoutCallbackURLs                  []string
	LaunchURL                           *string
	IsPublic                            bool
	PkceEnabled                         bool
	RequiresReauthentication            bool
	RequiresPushedAuthorizationRequests bool
	SkipConsent                         bool
	AccessTokenDurationMinutes          int64
	RefreshTokenDurationMinutes         int64
}

// List answers one page of the clients, newest first unless the caller sorts
// otherwise. Every client carries its secrets' views and its groups.
func (s *Service) List(ctx context.Context, search, sortBy string, ascending bool, page, limit int) ([]ClientView, responder.Pagination, error) {
	page, limit = responder.NormalizePage(page, limit, responder.DefaultPageSize, responder.MaxPageSize)
	rows, total, err := s.repo.ListClients(ctx, s.pool, search, sortBy, ascending, responder.Offset(page, limit), limit)
	if err != nil {
		return nil, responder.Pagination{}, err
	}

	views := make([]ClientView, 0, len(rows))
	for _, row := range rows {
		view := row.view(s.now())
		groups, groupErr := s.repo.ListAllowedGroups(ctx, s.pool, row.ID)
		if groupErr != nil {
			return nil, responder.Pagination{}, groupErr
		}
		view.AllowedGroups = groups
		view.LogoURL = s.logoURL(view)
		views = append(views, view)
	}
	return views, responder.NewPagination(responder.PaginationParams{Page: page, Limit: limit}, total), nil
}

// Get answers one client, secrets' views and groups included. An identifier
// that names no client — a malformed one included — is the not-found failure.
func (s *Service) Get(ctx context.Context, id string) (ClientView, error) {
	row, err := s.repo.GetClient(ctx, s.pool, id)
	if errors.Is(err, datastore.ErrNoRows) {
		return ClientView{}, ErrClientNotFound
	}
	if err != nil {
		return ClientView{}, err
	}
	view := row.view(s.now())
	view.AllowedGroups, err = s.repo.ListAllowedGroups(ctx, s.pool, id)
	if err != nil {
		return ClientView{}, err
	}
	view.LogoURL = s.logoURL(view)
	return view, nil
}

// Create defines a client and mints its first secret. The write, the
// restriction, and the audit record commit in one transaction, so a
// duplicate identifier or an unknown group leaves nothing behind.
//
// A public client cannot keep a secret, so PKCE is not its choice to make:
// the kind forces the requirement on, the way the protocol does.
func (s *Service) Create(ctx context.Context, callerID uuid.UUID, params CreateParams) (Issued, error) {
	id := params.ID
	if id == "" {
		id = uuid.NewV7().String()
	}

	secret, raw, err := s.newSecret(nil)
	if err != nil {
		return Issued{}, err
	}
	row := ClientSchema{
		ID:                                  id,
		Name:                                &params.Name,
		Description:                         params.Description,
		CallbackURLs:                        params.CallbackURLs,
		LogoutCallbackURLs:                  params.LogoutCallbackURLs,
		LaunchURL:                           params.LaunchURL,
		IsPublic:                            params.IsPublic,
		PkceEnabled:                         params.PkceEnabled || params.IsPublic,
		RequiresReauthentication:            params.RequiresReauthentication,
		RequiresPushedAuthorizationRequests: params.RequiresPushedAuthorizationRequests,
		SkipConsent:                         params.SkipConsent,
		ClientType:                          ClientTypeStandard,
		AccessTokenDurationMinutes:          durationOrDefault(params.AccessTokenDurationMinutes, DefaultAccessTokenMinutes),
		RefreshTokenDurationMinutes:         durationOrDefault(params.RefreshTokenDurationMinutes, DefaultRefreshTokenMinutes),
		CreatedByID:                         &callerID,
	}
	stored := credentials{Secrets: []Secret{secret}}
	encoded, encodeErr := json.Marshal(stored)
	if encodeErr != nil {
		return Issued{}, fmt.Errorf("oidc: encode credentials: %w", encodeErr)
	}
	row.Credentials = encoded

	var groupIDs []uuid.UUID
	if len(params.AllowedGroupWires) > 0 {
		groupIDs, err = parseGroupWires(params.AllowedGroupWires)
		if err != nil {
			return Issued{}, ErrGroupUnknown
		}
	}

	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if createErr := s.repo.CreateClient(ctx, tx, row); createErr != nil {
			if errUniqueViolation(createErr) {
				return ErrClientExists
			}
			return createErr
		}
		if len(groupIDs) > 0 {
			if groupErr := s.repo.SetAllowedGroups(ctx, tx, id, groupIDs); groupErr != nil {
				return groupErr
			}
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventOidcClientCreated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceOidcClient,
			Payload: map[string]string{
				"client_id":  id,
				"name":       params.Name,
				"is_public":  fmt.Sprintf("%t", params.IsPublic),
				"created_by": callerID.String(),
			},
		})
		return nil
	})
	if err != nil {
		return Issued{}, err
	}

	view, err := s.Get(ctx, id)
	if err != nil {
		return Issued{}, err
	}
	return Issued{Client: view, Secret: raw}, nil
}

// Update replaces a client's fields. The read and the write run in one
// transaction under the row lock, so a client deleted between them answers
// the not-found failure and a concurrent secret write is preserved — the
// rewrite never touches the credentials document.
func (s *Service) Update(ctx context.Context, id string, params UpdateParams) (ClientView, error) {
	var updated ClientView
	err := s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		row, err := s.lockClient(ctx, tx, id)
		if err != nil {
			return err
		}

		row.Name = &params.Name
		row.Description = params.Description
		row.CallbackURLs = params.CallbackURLs
		row.LogoutCallbackURLs = params.LogoutCallbackURLs
		row.LaunchURL = params.LaunchURL
		row.IsPublic = params.IsPublic
		// A public client cannot keep a secret, so PKCE is not its choice:
		// the kind forces the toggle on. The observed-capability flag dies
		// with the requirement it was observed beside — a client whose
		// requirement is off has shown nothing yet.
		row.PkceEnabled = params.PkceEnabled || params.IsPublic
		if !row.PkceEnabled {
			row.PkceSupported = false
		}
		row.RequiresReauthentication = params.RequiresReauthentication
		row.RequiresPushedAuthorizationRequests = params.RequiresPushedAuthorizationRequests
		row.SkipConsent = params.SkipConsent
		row.AccessTokenDurationMinutes = durationOrDefault(params.AccessTokenDurationMinutes, row.AccessTokenDurationMinutes)
		row.RefreshTokenDurationMinutes = durationOrDefault(params.RefreshTokenDurationMinutes, row.RefreshTokenDurationMinutes)

		if _, err := s.repo.UpdateClient(ctx, tx, row); err != nil {
			return err
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventOidcClientUpdated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceOidcClient,
			Payload:      map[string]string{"client_id": id, "name": params.Name},
		})
		updated = row.view(s.now())
		return nil
	})
	if err != nil {
		return ClientView{}, err
	}
	groups, err := s.repo.ListAllowedGroups(ctx, s.pool, id)
	if err != nil {
		return ClientView{}, err
	}
	updated.AllowedGroups = groups
	updated.LogoURL = s.logoURL(updated)
	return updated, nil
}

// Delete removes a client. The codes, sessions, grants, and restrictions
// that name it die with it, by the schema's cascades; the record names the
// client, which a reader can no longer look up.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		deleted, err := s.repo.DeleteClient(ctx, tx, id)
		if err != nil {
			return err
		}
		if !deleted {
			return ErrClientNotFound
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventOidcClientDeleted,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceOidcClient,
			// The client id is the operator's word, not a row UUID, so it
			// cannot ride the uuid resource_id column — the payload is the
			// only place a reader can match it from.
			Payload: map[string]string{"client_id": id},
		})
		return nil
	})
}

// SetAllowedGroups replaces the set of groups the client's restriction
// names. An empty list empties the restriction's roll: with the restriction
// on, the client admits nobody. Every identifier is parsed first, and one
// that names no group refuses the replacement whole.
func (s *Service) SetAllowedGroups(ctx context.Context, id string, wires []string) (ClientView, error) {
	ids, err := parseGroupWires(wires)
	if err != nil {
		return ClientView{}, ErrGroupUnknown
	}

	var updated ClientView
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if _, lockErr := s.lockClient(ctx, tx, id); lockErr != nil {
			return lockErr
		}
		if setErr := s.repo.SetAllowedGroups(ctx, tx, id, ids); setErr != nil {
			return setErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:        audit.EventOidcClientGroupsUpdated,
			Status:       audit.StatusSuccess,
			ResourceType: ResourceOidcClient,
			Payload:      map[string]string{"client_id": id, "group_count": fmt.Sprintf("%d", len(ids))},
		})
		return nil
	})
	if err != nil {
		return ClientView{}, err
	}
	if updated, err = s.Get(ctx, id); err != nil {
		return ClientView{}, err
	}
	return updated, nil
}

// Meta answers the display facts a sign-in page renders.
func (s *Service) Meta(ctx context.Context, id string) (MetaView, error) {
	view, err := s.Get(ctx, id)
	if err != nil {
		return MetaView{}, err
	}
	return MetaView{
		ID:                       view.ID,
		Name:                     view.Name,
		Description:              view.Description,
		LaunchURL:                view.LaunchURL,
		ClientType:               view.ClientType,
		HasLogo:                  view.HasLogo,
		RequiresReauthentication: view.RequiresReauthentication,
	}, nil
}

// lockClient reads one client under the row lock, so a read-modify-write —
// the rewrite, the restriction, the secrets document — holds the row
// against a concurrent procedure between its read and its write.
func (s *Service) lockClient(ctx context.Context, db datastore.Querier, id string) (ClientSchema, error) {
	row, err := s.repo.GetClientForUpdate(ctx, db, id)
	if errors.Is(err, datastore.ErrNoRows) {
		return ClientSchema{}, ErrClientNotFound
	}
	if err != nil {
		return ClientSchema{}, err
	}
	return row, nil
}

// durationOrDefault answers a window the request carried, or the default the
// caller named when it did not — the create's schema defaults, the update's
// values already on the row.
func durationOrDefault(value, fallback int64) int64 {
	if value == 0 {
		return fallback
	}
	return value
}

// parseGroupWires turns the restriction's wire identifiers into the UUIDs
// the junction stores. A malformed identifier names no group, and the whole
// replacement is refused for it.
func parseGroupWires(wires []string) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(wires))
	for _, wire := range wires {
		id, err := usergroup.UUIDFromWire(wire)
		if err != nil {
			return nil, ErrGroupUnknown
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// errUniqueViolation reports whether the write failed on a unique index, the
// way the primary key answers a duplicate client id.
func errUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
