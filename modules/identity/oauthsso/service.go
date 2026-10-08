package oauthsso

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"uuid"

	fwaudit "github.com/riipandi/saka/framework/audit"
	"github.com/riipandi/saka/framework/datastore"
	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/pkg/crypto"
)

// The failures the service reports. The handler maps them onto the codes
// the Connect protocol carries; the service defines what happened, not
// how it is answered.
var (
	// ErrInvalidConnection is a create or update whose fields do not
	// compose into a connection this feature can run a flow with: an
	// unknown builtin slug, a custom connection that rides neither a
	// discovery document nor manual endpoints, manual endpoints that are
	// not https, or a slug that collides with a builtin provider's.
	ErrInvalidConnection = errors.New("oauthsso: the connection's fields do not compose into a runnable connection")

	// ErrSecretUnavailable is a create or a secret replacement on a
	// process with no application cipher: the connection is refused
	// rather than stored with a secret in the clear.
	ErrSecretUnavailable = errors.New("oauthsso: the application secret is not configured")
)

// ConnectionParams is one create's payload, in the service's own words.
// The scopes travel space-separated — the wire's word for the set — and
// the service splits them.
type ConnectionParams struct {
	Kind             ConnectionKind
	Provider         string
	DisplayName      string
	DiscoveryURL     string
	Endpoints        Endpoints
	ClientID         string
	ClientSecret     string
	Scopes           string
	AttributeMapping AttributeMapping
	CustomAttributes []CustomAttribute
	Enabled          bool
}

// ConnectionUpdate is one update's payload. Every field is optional: a
// nil keeps the stored value, and the endpoint source — the discovery URL
// and the manual set together — is replaced as one, the way a create
// decides it.
type ConnectionUpdate struct {
	DisplayName      *string
	DiscoveryURL     *string
	Endpoints        *Endpoints
	ClientID         *string
	ClientSecret     *string
	Scopes           *string
	AttributeMapping *AttributeMapping
	// CustomAttributes rewrite the operator-defined attribute set as a
	// whole; a nil keeps the stored set, an empty slice clears it.
	CustomAttributes *[]CustomAttribute
	Enabled          *bool
}

// Service carries the rules of the connection surface: which field sets
// compose into a runnable connection, how a secret is sealed, and what a
// change leaves behind in the audit trail. The repository carries the
// SQL; the flow's begin and callback join this service in their phase.
type Service struct {
	pool    *datastore.Postgres
	repo    *Repository
	cipher  *crypto.Cipher
	audit   *fwaudit.Recorder
	fetcher DiscoveryFetcher
	log     *slog.Logger

	// providers are the adapters the flows run through, wired after
	// construction by the area: the feature root must not import the
	// adapter packages, which import it for its types. A missing adapter
	// — a builtin slug the set does not name, a nil custom adapter —
	// refuses at the begin rather than failing the run.
	providers ProviderSet

	// tokens is the form POST the token client presents the refresh
	// grant with, wired after construction like the adapters. A nil
	// poster makes every refresh a skip: the retrieval answers the
	// stored tokens, and the offboarding pass has nothing to probe with.
	tokens TokenPoster

	// issuer is the session mint the resolution binds accounts through,
	// the mfa gate the second factor's fork runs, settings the JIT
	// policy's runtime source, blocklist the identifier gate, and codes
	// the email code's delivery. Each rides the post-construction seam
	// like the adapters do; a nil issuer refuses every continue.
	issuer    signInIssuer
	mfa       mfaGate
	settings  settingsReader
	blocklist blocklistChecker
	codes     CodeNotifier
	// profiles is the account-write seam the mapping's refresh runs
	// through — the user module owns the row. Nil skips the refresh.
	profiles profileApplier
	// bans and ender are the offboarding's writes: the restrictions row
	// and the session lifecycle, satisfied structurally by their own
	// features' services.
	bans  banApplier
	ender sessionEnder

	// baseURL is the origin the redirect URI and the SPA redirect are
	// built from, wired after construction with the base URL.
	baseURL string

	// ssoEnabled is the configuration's master switch over the whole
	// surface, wired after construction. It defaults to on: a Service
	// built without the wiring (a test) has the surface open, the way
	// the connections it holds decide the rest.
	ssoEnabled bool

	// now is the instant the service's decisions read. It is a field so a
	// test can hold the clock still without waiting out a window.
	now func() time.Time
}

// ProviderSet is the adapter collection the area wires: the builtin
// adapters by slug, and the one generic OIDC adapter every custom
// connection shares.
type ProviderSet struct {
	Builtin map[string]Provider
	Custom  Provider
}

// WithProviders wires the adapter set the flows run through. The wiring
// rides the post-construction seam because the adapter packages import
// this one for its types.
func (s *Service) WithProviders(set ProviderSet) *Service {
	s.providers = set
	return s
}

// WithTokenPoster wires the form POST the token client presents the
// refresh grant with — the same post-construction seam the adapters ride.
func (s *Service) WithTokenPoster(poster TokenPoster) *Service {
	s.tokens = poster
	return s
}

// providerFor answers the adapter a connection's kind and slug run
// through. A connection whose adapter the set does not name is a wiring
// defect the flow refuses at the begin — the same fail-closed answer a
// missing feature service gets.
func (s *Service) providerFor(conn Connection) (Provider, error) {
	switch conn.Kind {
	case KindBuiltin:
		if provider, ok := s.providers.Builtin[conn.Provider]; ok && provider != nil {
			return provider, nil
		}
	case KindCustom:
		if s.providers.Custom != nil {
			return s.providers.Custom, nil
		}
	}
	return nil, fmt.Errorf("oauthsso: no provider adapter is wired for %s connection %q", conn.Kind, conn.Provider)
}

// NewService builds the feature. A nil cipher is a deployment without an
// application secret: reads and deletes serve, a create or a secret
// replacement is refused at the call site. A nil fetcher leaves the
// discovery validation unavailable — a custom connection that rides a
// discovery document is refused until the outbound client is wired, the
// state a bare wiring is in.
func NewService(pool *datastore.Postgres, cipher *crypto.Cipher, recorder *fwaudit.Recorder, fetcher DiscoveryFetcher, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		pool:       pool,
		repo:       NewRepository(pool),
		cipher:     cipher,
		audit:      recorder,
		fetcher:    fetcher,
		log:        log,
		ssoEnabled: true,
		now:        time.Now,
	}
}

// List answers every live connection in provider order.
func (s *Service) List(ctx context.Context) ([]Connection, error) {
	return s.repo.List(ctx)
}

// EnabledConnection is one connection an unauthenticated caller may sign
// in with: the slug the start route names and the display name the page
// renders — the button's label and its destination, nothing else.
type EnabledConnection struct {
	Provider    string
	DisplayName string
}

// EnabledConnections answers the connections an unauthenticated caller may
// sign in with, ordered by provider. The configuration's master switch
// gates the surface: while it is off, the answer is empty whatever the
// rows hold — the switch is the restart-gated word, the rows are the
// runtime ones.
func (s *Service) EnabledConnections(ctx context.Context) ([]EnabledConnection, error) {
	if !s.ssoEnabled {
		return []EnabledConnection{}, nil
	}
	rows, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]EnabledConnection, 0, len(rows))
	for _, conn := range rows {
		if conn.Enabled {
			out = append(out, EnabledConnection{Provider: conn.Provider, DisplayName: conn.DisplayName})
		}
	}
	return out, nil
}

// Get reads one connection by its identifier.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Connection, error) {
	return s.repo.ByID(ctx, s.pool, id)
}

// ByProvider reads one connection by its provider slug — the resolution's
// lookup, and the read the flow's begin rides.
func (s *Service) ByProvider(ctx context.Context, provider string) (Connection, error) {
	return s.repo.ByProvider(ctx, s.pool, provider)
}

// Create validates, seals, and stores one connection. The discovery
// document a custom connection rides is fetched and validated before the
// row is written — an endpoint set the flow cannot run must not become a
// stored row — and the resolved endpoints are what the row stores, so the
// flow time never re-fetches.
func (s *Service) Create(ctx context.Context, params ConnectionParams) (Connection, error) {
	conn, err := s.build(ctx, params)
	if err != nil {
		return Connection{}, err
	}
	secret, err := s.seal(params.ClientSecret)
	if err != nil {
		return Connection{}, err
	}
	conn.ClientSecret = secret

	var created Connection
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		id, createdAt, insertErr := s.repo.Insert(ctx, tx, conn)
		if insertErr != nil {
			return insertErr
		}
		conn.ID, conn.CreatedAt = id, createdAt
		s.record(ctx, tx, audit.EventOauthSsoConnectionCreated, conn,
			map[string]string{"provider": conn.Provider, "kind": string(conn.Kind)})
		created = conn
		return nil
	})
	if err != nil {
		return Connection{}, err
	}
	return created, nil
}

// Update rewrites one connection's editable fields. A nil field keeps the
// stored value; a new client secret rides only when the stored one is
// being replaced. A rewritten endpoint source is validated the way a
// create is — the row's endpoints are what the flow runs against, and a
// stale resolution must not survive the edit.
func (s *Service) Update(ctx context.Context, id uuid.UUID, update ConnectionUpdate) (Connection, error) {
	current, err := s.repo.ByID(ctx, s.pool, id)
	if err != nil {
		return Connection{}, err
	}

	merged := current
	if update.DisplayName != nil {
		merged.DisplayName = *update.DisplayName
	}
	// The endpoint source is rewritten as one: naming either the discovery
	// URL or the manual set clears the other, and the merged pair is then
	// resolved and validated together.
	if update.DiscoveryURL != nil || update.Endpoints != nil {
		merged.DiscoveryURL = ""
		merged.Endpoints = Endpoints{}
		if update.DiscoveryURL != nil {
			merged.DiscoveryURL = *update.DiscoveryURL
		}
		if update.Endpoints != nil {
			merged.Endpoints = *update.Endpoints
		}
	}
	if update.ClientID != nil {
		merged.ClientID = *update.ClientID
	}
	if update.Scopes != nil {
		merged.Scopes = splitScopes(*update.Scopes)
	}
	if update.AttributeMapping != nil {
		merged.AttributeMapping = mergeMapping(current.AttributeMapping, *update.AttributeMapping)
	}
	if update.CustomAttributes != nil {
		merged.CustomAttributes = *update.CustomAttributes
	}
	if update.Enabled != nil {
		merged.Enabled = *update.Enabled
	}

	if merged.Kind == KindCustom {
		endpoints, resolveErr := s.resolveEndpoints(ctx, merged.DiscoveryURL, merged.Endpoints)
		if resolveErr != nil {
			return Connection{}, resolveErr
		}
		merged.Endpoints = endpoints
	}
	if update.ClientSecret != nil && *update.ClientSecret != "" {
		secret, sealErr := s.seal(*update.ClientSecret)
		if sealErr != nil {
			return Connection{}, sealErr
		}
		merged.ClientSecret = secret
	}
	if validErr := validateStored(merged); validErr != nil {
		return Connection{}, validErr
	}

	var updated Connection
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		updatedAt, updateErr := s.repo.Update(ctx, tx, id, merged)
		if updateErr != nil {
			return updateErr
		}
		merged.UpdatedAt = &updatedAt
		s.record(ctx, tx, audit.EventOauthSsoConnectionUpdated, merged,
			map[string]string{"provider": merged.Provider})
		updated = merged
		return nil
	})
	if err != nil {
		return Connection{}, err
	}
	return updated, nil
}

// Delete removes one connection and, through the foreign keys, the linked
// accounts and the live flows that rode it.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		conn, err := s.repo.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		deleted, err := s.repo.Delete(ctx, tx, id)
		if err != nil {
			return err
		}
		if !deleted {
			return ErrConnectionNotFound
		}
		s.record(ctx, tx, audit.EventOauthSsoConnectionDeleted, conn,
			map[string]string{"provider": conn.Provider})
		return nil
	})
}

// build turns a create's payload into a storable connection: it names the
// kind's rules, resolves the endpoint source, and splits the scopes. The
// secret is left for the caller to seal — the failure is answered beside
// the write, not inside the composition.
func (s *Service) build(ctx context.Context, params ConnectionParams) (Connection, error) {
	conn := Connection{
		Kind:             params.Kind,
		Provider:         strings.ToLower(strings.TrimSpace(params.Provider)),
		DisplayName:      params.DisplayName,
		DiscoveryURL:     params.DiscoveryURL,
		Endpoints:        params.Endpoints,
		ClientID:         params.ClientID,
		Scopes:           splitScopes(params.Scopes),
		AttributeMapping: params.AttributeMapping,
		CustomAttributes: params.CustomAttributes,
		Enabled:          params.Enabled,
	}

	switch conn.Kind {
	case KindBuiltin:
		// The endpoints are the code's own: a builtin create carries its
		// credentials and nothing else, and the slug must name an
		// adapter the wiring shipped.
		if _, ok := s.providers.Builtin[conn.Provider]; !ok {
			return Connection{}, fmt.Errorf("%w: %q does not name a builtin provider", ErrInvalidConnection, conn.Provider)
		}
		if conn.DiscoveryURL != "" || conn.Endpoints.Authorization != "" || conn.Endpoints.Token != "" {
			return Connection{}, fmt.Errorf("%w: a builtin connection carries no endpoints of its own", ErrInvalidConnection)
		}
		// An empty scope list is the adapter's own default set, read at
		// the authorize request — the definition owns it, not the row.
		conn.AttributeMapping = AttributeMapping{}
		conn.CustomAttributes = nil
	case KindCustom:
		// The builtin slugs are reserved: a custom connection named
		// `google` would answer BeginSignIn with the operator's endpoints
		// under the code's name.
		if _, reserved := s.providers.Builtin[conn.Provider]; reserved {
			return Connection{}, fmt.Errorf("%w: %q is a builtin provider's slug", ErrInvalidConnection, conn.Provider)
		}
		if conn.Provider == "" {
			return Connection{}, fmt.Errorf("%w: the provider slug is required", ErrInvalidConnection)
		}
		if conn.DiscoveryURL != "" && conn.Endpoints.Authorization != "" {
			return Connection{}, fmt.Errorf("%w: a connection rides one endpoint source — a discovery URL or manual endpoints, never both", ErrInvalidConnection)
		}
		endpoints, err := s.resolveEndpoints(ctx, conn.DiscoveryURL, conn.Endpoints)
		if err != nil {
			return Connection{}, err
		}
		conn.Endpoints = endpoints
	default:
		return Connection{}, fmt.Errorf("%w: kind %q is neither builtin nor custom", ErrInvalidConnection, conn.Kind)
	}

	if err := validateStored(conn); err != nil {
		return Connection{}, err
	}
	return conn, nil
}

// resolveEndpoints answers the endpoint set a connection stores: the
// manual set when it carries one, the discovery document's resolution
// when it rides a URL instead.
func (s *Service) resolveEndpoints(ctx context.Context, discoveryURL string, manual Endpoints) (Endpoints, error) {
	if discoveryURL != "" {
		return ResolveDiscovery(ctx, s.fetcher, discoveryURL)
	}
	if err := validateManualEndpoints(manual); err != nil {
		return Endpoints{}, fmt.Errorf("%w: %v", ErrInvalidConnection, err)
	}
	return manual, nil
}

// seal renders the secret's stored form. A process without the
// application cipher refuses rather than storing the clear text.
func (s *Service) seal(secret string) (string, error) {
	if s.cipher == nil {
		return "", ErrSecretUnavailable
	}
	sealed, err := s.cipher.Encrypt(secret)
	if err != nil {
		return "", fmt.Errorf("oauthsso: seal client secret: %w", err)
	}
	return sealed, nil
}

// validateStored holds a composed connection to what the row must carry:
// the identifiers the surface renders and the endpoints the flow runs
// against. The client secret is judged beside the write — it arrives
// unsealed in a build and sealed in an update — and a sealed form is
// never empty. A builtin connection's endpoints live in the code, so
// only a custom one's are judged here.
func validateStored(conn Connection) error {
	if conn.Provider == "" || conn.DisplayName == "" || conn.ClientID == "" {
		return fmt.Errorf("%w: the provider slug, the display name, and the client id are required", ErrInvalidConnection)
	}
	if conn.Kind == KindCustom {
		if err := validateManualEndpoints(conn.Endpoints); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConnection, err)
		}
	}
	if err := validateCustomAttributes(conn.CustomAttributes); err != nil {
		return err
	}
	return nil
}

// validateCustomAttributes holds the operator-defined attribute set to
// what a connection may store: every entry names its key and its claim,
// and no key repeats — a repeated key would make one of the values
// unreadable.
func validateCustomAttributes(attrs []CustomAttribute) error {
	seen := make(map[string]struct{}, len(attrs))
	for _, attr := range attrs {
		if attr.Key == "" || attr.Claim == "" {
			return fmt.Errorf("%w: every custom attribute names its key and its claim", ErrInvalidConnection)
		}
		if _, dup := seen[attr.Key]; dup {
			return fmt.Errorf("%w: the custom attribute key %q repeats", ErrInvalidConnection, attr.Key)
		}
		seen[attr.Key] = struct{}{}
	}
	return nil
}

// splitScopes breaks the wire's space-separated set into the stored list.
func splitScopes(scopes string) []string {
	fields := strings.Fields(scopes)
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// mergeMapping folds an update's mapping onto the stored one; an empty
// field keeps the stored claim name.
func mergeMapping(current, update AttributeMapping) AttributeMapping {
	merged := current
	if update.Email != "" {
		merged.Email = update.Email
	}
	if update.GivenName != "" {
		merged.GivenName = update.GivenName
	}
	if update.FamilyName != "" {
		merged.FamilyName = update.FamilyName
	}
	return merged
}

// record writes the change's audit record inside the causing transaction.
// The payload names the provider slug and the connection's wire form —
// the resource_id column is a UUID and the wire identifier is not, so the
// payload is the only place a reader can match it from — and carries
// nothing secret. The recorder reads the client facts and the
// impersonation actor from the context; the service carries none of them.
func (s *Service) record(ctx context.Context, tx datastore.Querier, event string, conn Connection, payload map[string]string) {
	out := make(map[string]string, len(payload)+1)
	for key, value := range payload {
		out[key] = value
	}
	out["connection_id"] = FormatID(conn.ID)
	s.audit.Record(ctx, tx, fwaudit.Entry{
		Event:        event,
		Trigger:      fwaudit.TriggerUser,
		Status:       fwaudit.StatusSuccess,
		ResourceType: ResourceOAuthConnection,
		Payload:      out,
	})
}
