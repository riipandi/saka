package scimsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"
	"uuid"

	"github.com/huandu/go-sqlbuilder"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/database/entity"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/internal/fetcher"
	"github.com/riipandi/saka/pkg/crypto"
)

// The SCIM 2.0 core schemas the sync speaks. A payload names the one schema
// it carries; a list answer names the `ListResponse` wrapper beside it.
const (
	scimUserSchema     = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimGroupSchema    = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimListSchema     = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimContentType    = "application/scim+json"
	scimPageCount      = 1000
	scimErrorBodyLimit = 4 << 10

	// The bounds one listing may walk before the pass refuses it. A
	// remote that reports endless results, or pages that never advance,
	// would otherwise accumulate without limit; 100 pages of the 1000
	// rows a page may carry is 100,000 resources, far above any
	// deployment this sync targets, and the answer is an error the pass
	// reports — never a silently truncated snapshot.
	scimMaxPages     = 100
	scimMaxResources = 100 * scimPageCount
)

// ErrNoDirectory reports a service built without an account or group
// source: the sync has nothing to push. It is answered at the call site —
// a run without the identity area skips its sync jobs rather than failing.
var ErrNoDirectory = errors.New("scimsync: no account directory wired")

// Service runs the outbound provisioning passes and administers the
// provider rows.
type Service struct {
	repo       *Repository
	pool       *datastore.Postgres
	recorder   *audit.Recorder
	log        *slog.Logger
	cipher     *crypto.Cipher
	httpClient HTTPClient
	users      Directory
	groups     GroupDirectory
}

// HTTPClient is the outbound seam. The fetcher client satisfies it; the
// tests replace it with a stub that records what the sync sent.
type HTTPClient interface {
	Do(ctx context.Context, req fetcher.Request) (*fetcher.Response, error)
}

// Directory is the account facts the sync pushes, the user feature's
// method set narrowed to what a provisioning pass reads.
type Directory interface {
	// UsersForClient answers every account a client's visibility roll
	// admits, newest last, with the client's restriction already applied.
	UsersForClient(ctx context.Context, db datastore.Querier, clientID string) ([]ProvisionedUser, error)
}

// GroupDirectory is the same seam for groups.
type GroupDirectory interface {
	// GroupsForClient answers every group the client's visibility roll
	// admits, each with the members the roll admits.
	GroupsForClient(ctx context.Context, db datastore.Querier, clientID string) ([]ProvisionedGroup, error)
}

// ClientRestriction is the client-side visibility roll the sync applies.
// It mirrors the OIDC authorization's semantics — an unrestricted client
// sees everyone, a restricted one sees its allowed groups' members — so
// provisioning cannot admit an account the sign-in would refuse.
type ClientRestriction struct {
	IsGroupRestricted bool
	AllowedGroupIDs   []uuid.UUID
}

// ProvisionedUser is one account's sync shape.
type ProvisionedUser struct {
	ID          uuid.UUID
	Username    string
	Email       string
	FirstName   string
	LastName    string
	DisplayName string
	Active      bool
	UpdatedAt   *time.Time
}

// ProvisionedGroup is one group's sync shape, members included.
type ProvisionedGroup struct {
	ID          uuid.UUID
	DisplayName string
	MemberIDs   []uuid.UUID
	UpdatedAt   *time.Time
}

// Stats is one pass's outcome, the counts it produced.
type Stats struct {
	UsersCreated  int
	UsersUpdated  int
	UsersDeleted  int
	GroupsCreated int
	GroupsUpdated int
	GroupsDeleted int
}

// NewService builds the service over the shared pool. users and groups may
// be nil — a run without the identity area answers ErrNoDirectory on sync
// and the management procedures still work. A nil cipher unseals nothing:
// the sync fails closed on the first pass, and a Create refuses.
func NewService(pool *datastore.Postgres, repo *Repository, recorder *audit.Recorder, cipher *crypto.Cipher, httpClient HTTPClient, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		repo:       repo,
		pool:       pool,
		recorder:   recorder,
		cipher:     cipher,
		httpClient: httpClient,
		log:        log,
	}
}

// WithDirectories wires the account and group sources. The seam rides
// post-construction wiring like the other cross-area dependencies.
func (s *Service) WithDirectories(users Directory, groups GroupDirectory) *Service {
	s.users = users
	s.groups = groups
	return s
}

// GetByClient answers the provider one client syncs to.
func (s *Service) GetByClient(ctx context.Context, clientID string) (Provider, error) {
	return s.repo.ByClient(ctx, s.pool, clientID)
}

// Create attaches a provisioning target to a client and answers the row
// with the token's plaintext — the operator's one look before it seals.
func (s *Service) Create(ctx context.Context, clientID, endpoint, token string) (Provider, error) {
	if s.cipher == nil {
		return Provider{}, errors.New("scimsync: no secret key is configured to seal the token")
	}
	if _, err := s.clientExists(ctx, clientID); err != nil {
		return Provider{}, err
	}
	sealed, err := s.cipher.Encrypt(token)
	if err != nil {
		return Provider{}, fmt.Errorf("scimsync: seal token: %w", err)
	}

	created, err := s.repo.Create(ctx, s.pool, Provider{
		ID:          uuid.NewV7(),
		ClientID:    clientID,
		Endpoint:    endpoint,
		SealedToken: sealed,
	})
	if err != nil {
		return Provider{}, err
	}

	created.SealedToken = token
	s.record(ctx, audit.EventScimProviderCreated, created.ID.String(), clientID)
	return created, nil
}

// Update replaces a provider's endpoint and token. An empty token keeps
// the stored one — a rotation is a value the caller supplies.
func (s *Service) Update(ctx context.Context, id uuid.UUID, endpoint, token string) (Provider, error) {
	existing, err := s.repo.ByID(ctx, s.pool, id)
	if err != nil {
		return Provider{}, err
	}
	sealed := existing.SealedToken
	if token != "" {
		if s.cipher == nil {
			return Provider{}, errors.New("scimsync: no secret key is configured to seal the token")
		}
		resealed, sealErr := s.cipher.Encrypt(token)
		if sealErr != nil {
			return Provider{}, fmt.Errorf("scimsync: seal token: %w", sealErr)
		}
		sealed = resealed
	}

	updated, err := s.repo.Update(ctx, s.pool, Provider{
		ID:          id,
		ClientID:    existing.ClientID,
		Endpoint:    endpoint,
		SealedToken: sealed,
	})
	if err != nil {
		return Provider{}, err
	}

	s.record(ctx, audit.EventScimProviderUpdated, id.String(), existing.ClientID)
	return updated, nil
}

// Delete removes the provisioning target. The remote data stays where it
// is; deleting the row only ends the sync.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.ByID(ctx, s.pool, id)
	if err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, s.pool, id); err != nil {
		return err
	}
	s.record(ctx, audit.EventScimProviderDeleted, id.String(), existing.ClientID)
	return nil
}

// SyncAll runs one pass per provider. One provider's failure never stops
// the others; the joined errors are the caller's report.
func (s *Service) SyncAll(ctx context.Context) error {
	providers, err := s.repo.All(ctx, s.pool)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range providers {
		if _, err := s.Sync(ctx, p.ID); err != nil {
			errs = append(errs, fmt.Errorf("provider %s: %w", p.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Sync runs one provisioning pass now: the client's visible accounts and
// groups are created, updated, and removed on the remote until it matches
// the local snapshot. The pass reads one consistent local snapshot, then
// talks to the remote outside any transaction.
func (s *Service) Sync(ctx context.Context, id uuid.UUID) (Stats, error) {
	start := time.Now()

	snapshot, err := s.loadSnapshot(ctx, id)
	if err != nil {
		return Stats{}, err
	}

	var stats Stats
	if err := s.reconcile(ctx, snapshot, &stats); err != nil {
		s.log.WarnContext(ctx, "scimsync: pass completed with errors",
			"provider_id", id, "err", err, "duration", time.Since(start))
		return stats, err
	}

	if err := s.repo.StampSynced(ctx, s.pool, id, time.Now()); err != nil {
		return stats, err
	}
	s.recordWithStats(ctx, audit.EventScimSyncCompleted, snapshot.provider, stats)
	s.log.InfoContext(ctx, "scimsync: pass completed",
		"provider_id", id,
		"users_created", stats.UsersCreated, "users_updated", stats.UsersUpdated,
		"users_deleted", stats.UsersDeleted,
		"groups_created", stats.GroupsCreated, "groups_updated", stats.GroupsUpdated,
		"groups_deleted", stats.GroupsDeleted,
		"duration", time.Since(start))
	return stats, nil
}

// snapshot is one pass's inputs, read at one point in time.
type snapshot struct {
	provider Provider
	token    string
	users    []ProvisionedUser
	groups   []ProvisionedGroup
}

// loadSnapshot reads the provider, its unsealed token, and the client's
// visible accounts and groups.
func (s *Service) loadSnapshot(ctx context.Context, id uuid.UUID) (snapshot, error) {
	provider, err := s.repo.ByID(ctx, s.pool, id)
	if err != nil {
		return snapshot{}, err
	}
	if s.cipher == nil {
		return snapshot{}, errors.New("scimsync: no secret key is configured to unseal the token")
	}
	token, err := s.cipher.Decrypt(provider.SealedToken)
	if err != nil {
		return snapshot{}, fmt.Errorf("scimsync: unseal token: %w", err)
	}
	if s.users == nil || s.groups == nil {
		return snapshot{}, ErrNoDirectory
	}

	users, err := s.users.UsersForClient(ctx, s.pool, provider.ClientID)
	if err != nil {
		return snapshot{}, fmt.Errorf("scimsync: read the client's accounts: %w", err)
	}
	groups, err := s.groups.GroupsForClient(ctx, s.pool, provider.ClientID)
	if err != nil {
		return snapshot{}, fmt.Errorf("scimsync: read the client's groups: %w", err)
	}
	return snapshot{provider: provider, token: token, users: users, groups: groups}, nil
}

// clientExists refuses a create whose client does not exist. The client
// table is federation's; the query here is the one fact the sync needs.
func (s *Service) clientExists(ctx context.Context, clientID string) (bool, error) {
	sb := sqlbuilder.PostgreSQL.NewSelectBuilder()
	sb.Select("1")
	sb.From(entity.TableOIDCClients)
	sb.Where(sb.Equal("id", clientID))
	query, args := sb.Build()

	var one int
	err := s.pool.QueryRow(ctx, query, args...).Scan(&one)
	if errors.Is(err, datastore.ErrNoRows) {
		return false, ErrNoProvider
	}
	if err != nil {
		return false, fmt.Errorf("scimsync: read the client: %w", err)
	}
	return true, nil
}

// record writes one audit event. A nil recorder — a run built without the
// audit area — is answered by the recorder itself: recording is a side
// effect, and a feature that does not have one must still run.
func (s *Service) record(ctx context.Context, event, providerID, clientID string) {
	if s.recorder == nil {
		return
	}
	s.recorder.Record(ctx, s.pool, audit.Entry{
		Event:        event,
		Trigger:      audit.TriggerUser,
		Status:       audit.StatusSuccess,
		ResourceType: ResourceProvider,
		ResourceID:   providerID,
		Payload:      map[string]string{"client_id": clientID},
	})
}

// recordWithStats names the pass's outcome in the payload, so the log and
// the audit trail tell the same story.
func (s *Service) recordWithStats(ctx context.Context, event string, provider Provider, stats Stats) {
	if s.recorder == nil {
		return
	}
	s.recorder.Record(ctx, s.pool, audit.Entry{
		Event:        event,
		Trigger:      audit.TriggerSystem,
		Status:       audit.StatusSuccess,
		ResourceType: ResourceProvider,
		ResourceID:   provider.ID.String(),
		Payload: map[string]string{
			"client_id":      provider.ClientID,
			"users_created":  strconv.Itoa(stats.UsersCreated),
			"users_updated":  strconv.Itoa(stats.UsersUpdated),
			"users_deleted":  strconv.Itoa(stats.UsersDeleted),
			"groups_created": strconv.Itoa(stats.GroupsCreated),
			"groups_updated": strconv.Itoa(stats.GroupsUpdated),
			"groups_deleted": strconv.Itoa(stats.GroupsDeleted),
		},
	})
}
