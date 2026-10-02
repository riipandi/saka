// Package identity is the identity area: the features that establish who a
// caller is and what they may do.
//
// The area is the unit the composition root loads. It mounts every feature it
// holds on one router, so the registry names one module per area rather than
// one per feature, and a new identity feature is added here without the
// transport or the registry learning about it.
//
// The area also owns the wiring of its own services: which service a feature
// is built from, and which of them must be validated before the listener
// opens. That knowledge lives here, not in the composition root, so an area can
// be added to a running server by naming it rather than by teaching the
// registry about its internals.
package identity

import (
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"

	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/cache"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/fetcher"
	"github.com/riipandi/tango/internal/guard"
	"github.com/riipandi/tango/internal/jobs"
	"github.com/riipandi/tango/internal/kernel"
	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/internal/queue"
	"github.com/riipandi/tango/internal/storage"
	"github.com/riipandi/tango/modules/appconfig"
	"github.com/riipandi/tango/modules/devicelogin"
	"github.com/riipandi/tango/modules/identity/authorization"
	"github.com/riipandi/tango/modules/identity/blocklist"
	"github.com/riipandi/tango/modules/identity/jwks"
	"github.com/riipandi/tango/modules/identity/multifactor"
	"github.com/riipandi/tango/modules/identity/oauthsso"
	"github.com/riipandi/tango/modules/identity/onetimeaccess"
	"github.com/riipandi/tango/modules/identity/password"
	"github.com/riipandi/tango/modules/identity/session"
	"github.com/riipandi/tango/modules/identity/signin"
	"github.com/riipandi/tango/modules/identity/signup"
	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/modules/identity/usergroup"
	"github.com/riipandi/tango/modules/identity/verification"
	"github.com/riipandi/tango/modules/identity/webauthn"
	"github.com/riipandi/tango/pkg/crypto"
	"github.com/riipandi/tango/pkg/jwtutils"
)

// ModuleName is the name the area reports under.
const ModuleName = "identity"

// Deps are the resolved services the area's features are built from. The
// registry resolves them; the area decides which feature takes which, so a
// feature's dependencies are named here rather than at the call site.
type Deps struct {
	// KeySet supplies the keys the JSON Web Key Set publishes. It is the
	// provider interface rather than the service, so the cache in front of
	// the service is invisible to the feature.
	KeySet jwtutils.KeyProvider

	// SignIn verifies the primary credential and issues the token pair.
	SignIn *signin.Service

	// Sessions carries the lifecycle of the session a sign-in opened.
	Sessions *session.Service

	// Signup creates an account from a signup token.
	Signup *signup.Service

	// Users administers the accounts.
	Users *user.Service

	// Verification verifies an account's address over the mailer and the
	// queue.
	Verification *verification.Service

	// OneTimeAccess issues and consumes the codes that sign an account in
	// without its password.
	OneTimeAccess *onetimeaccess.Service

	// Multifactor is the second factor: TOTP authenticators and the recovery
	// codes. Its sign-in fork is wired to the sign-in service at build time.
	Multifactor *multifactor.Service

	// WebAuthn is the passkey surface: enrollment, the usernameless sign-in,
	// and the credential roll. Its ceremonies read their limits through the
	// settings feature, and its TOTP-count seam is wired to the multifactor
	// service at build time.
	WebAuthn *webauthn.Service

	// PasswordRecovery is the forgot-password flow: reset tokens, the
	// emails that carry them, and the swap a spent token buys.
	PasswordRecovery *password.Service

	// DeviceLogin is the passkey-less pairing sign-in: the REST surface
	// the creating browser polls and the approval procedures the other
	// device answers. Nil when the feature is off.
	DeviceLogin *devicelogin.Service

	// OAuthSSO is the sign-in-with-a-provider feature: the connection
	// CRUD the operator drives, the outbound flow the browser crosses,
	// and the linked accounts the resolution binds. Nil when the feature
	// is off.
	OAuthSSO *oauthsso.Service

	// ExposeResetToken mirrors `app.expose_reset_token`, gated on the
	// development mode: the deployment's decision whether ForgotPassword
	// answers the raw token.
	ExposeResetToken bool

	// UserGroups administers the groups accounts belong to.
	UserGroups *usergroup.Service

	// Authorization administers the roles, the permission catalog's mirror,
	// and the grants that bind them to accounts.
	Authorization *authorization.Service

	// Blocklist is the sign-up blocklist: the entries, and the gate the
	// sign-up and sign-in features ask through their seams. A nil service
	// leaves every gate open.
	Blocklist *blocklist.Service

	// Storage is the file engine the profile pictures live in. A nil engine
	// leaves the picture procedures refusing while the account procedures
	// serve as usual.
	Storage *storage.Manager
}

// Module mounts every identity feature.
type Module struct {
	features []kernel.Module
}

// NewModule builds the area over its dependencies.
func NewModule(deps Deps) *Module {
	return &Module{features: features(deps)}
}

// Name reports the area in composition reports and logs.
func (m *Module) Name() string { return ModuleName }

// Mount registers every feature's endpoints on the router. It runs once, at
// startup, before the listener opens.
//
// The features mount in the order features returns. chi replaces the handler
// of a pattern registered twice, so the last one wins; two features claiming
// the same route is a defect to fix, not an ordering to rely on.
func (m *Module) Mount(r chi.Router) {
	kernel.Mount(r, m.features...)
}

// MountRPC registers the procedures of every feature that serves any. The
// area forwards because the composition root names the area alone: a
// feature's procedures reach the RPC router through the same seam its HTTP
// routes do, and the transport never learns a feature's name.
func (m *Module) MountRPC(r chi.Router, opts ...connect.HandlerOption) {
	kernel.MountRPC(r, opts, m.features...)
}

// Package registers the services this area owns.
//
// The composition root applies it while the container is built, so it only
// registers: each service is constructed when something resolves it, which is
// what keeps a run from dialling a database it never reads.
var Package = do.Package(
	do.Lazy(func(i do.Injector) (*jwks.Service, error) {
		c := do.MustInvoke[*config.Config](i)
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		// A nil pool is the area-test state: the wiring test resolves the
		// area without a database, and a source over nothing must answer
		// "no rows" rather than dereference the pool. The composition root
		// never passes one, so this is a guard, not a path.
		// The cipher unseals a stored private key for the OAuth
		// provider and the internal signing path; it is the auth
		// half — derived from AUTH_SECRET_KEY, not the application
		// secret — so rotating the auth secret invalidates the
		// sealed rows (the service retires and re-provisions them)
		// while rotating the application secret leaves the signing
		// keys untouched. A run without the auth secret has nothing
		// to open, and the signing paths answer that state on the
		// first call rather than failing a run that never serves.
		var cipher *crypto.Cipher
		if c.Auth.SecretKey != "" {
			built, err := crypto.NewAuthCipher(c.Auth.SecretKey)
			if err != nil {
				return nil, fmt.Errorf("identity: jwks cipher: %w", err)
			}
			cipher = built
		}
		var source jwks.Source
		if pool != nil {
			// The repository seals generated pairs with the same cipher the
			// service unseals with, so every row it writes carries the
			// current fingerprint.
			source = jwks.NewRepository(pool, cipher)
		}
		service := jwks.NewService(*c, source, cipher, log)
		// The auto-invalidation write path: a row sealed by a previous
		// AUTH_SECRET_KEY is retired and a replacement provisioned on the
		// next signing read, so the rotation recovers without downtime.
		// The replacement's algorithm follows the configured override or
		// the package default, mirroring initialize's choice.
		if repo, ok := source.(*jwks.Repository); ok {
			algorithm := c.Auth.JWTAlgorithm
			if algorithm == "" || config.IsHMACAlgorithm(algorithm) {
				algorithm = crypto.DefaultSignatureAlgorithm
			}
			recorder := do.MustInvoke[*audit.Recorder](i)
			service.WithSealer(repo, repo.GeneratePair, algorithm, recorder)
		}
		// The derived algorithm is worth a line at startup: when
		// auth.jwt_algorithm is unset, the material decides, and the
		// HMAC half decides by the secret's length alone. A deployment
		// that replaced AUTH_SECRET_KEY with a different-sized value
		// would change the algorithm silently; the log makes that
		// visible without a second configuration key.
		if err := service.Err(); err != nil {
			return nil, fmt.Errorf("identity: jwks: %w", err)
		}
		if alg, err := service.SigningAlgorithm(); err == nil {
			log.Info("jwks: signing algorithm resolved", "algorithm", alg.String(),
				"source", resolvedAlgorithmSource(c))
		} else if errors.Is(err, jwks.ErrNoSigningKey) || errors.Is(err, jwks.ErrNoStoredKeys) {
			// A table with no rows yet is a fresh database: initialize
			// provisions, and the run still serves — signing answers the
			// missing key on the first call that needs it.
			log.Warn("jwks: no signing key yet; run tango initialize")
		} else {
			return nil, fmt.Errorf("identity: jwks: resolve algorithm: %w", err)
		}
		return service, nil
	}),

	// The published key set is read behind a cache: a client that verifies
	// many tokens must not turn each verification into a query, and a
	// rotation is still picked up within the TTL. The cache is wired here
	// rather than inside the service, because how long a key set is reused
	// is a deployment decision, not a property of the set.
	do.Lazy(func(i do.Injector) (jwtutils.KeyProvider, error) {
		service := do.MustInvoke[*jwks.Service](i)
		return jwtutils.NewCachedKeyProvider(service, jwks.KeyCacheTTL), nil
	}),

	do.Lazy(func(i do.Injector) (*signin.Service, error) {
		c := do.MustInvoke[*config.Config](i)
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		keys := do.MustInvoke[*jwks.Service](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		client := do.MustInvoke[*queue.Client](i)
		service := signin.NewService(*c, pool, signin.NewRepository(pool), keys, recorder, log).
			WithDeviceNotifier(jobs.NewDeviceNotifier(client, log, c.Mailer.Notifications.NewDeviceNoticeEnabled))
		// The session bound reads the catalog at mint time; a nil settings
		// feature keeps the catalog default, the state a bare wiring is in.
		if settings := do.MustInvoke[*appconfig.Settings](i); settings != nil {
			service.WithSessionSettings(settings)
		}
		if policy := do.MustInvoke[*password.Validator](i); policy != nil {
			service.WithPasswordPolicy(policy)
		}
		return service, nil
	}),
	// The session lifecycle builds over the sign-in issuer through the
	// interface the session package defines — the renewal and the opening
	// must not drift apart, and the issuer satisfies it without an adapter.
	do.Lazy(func(i do.Injector) (*session.Service, error) {
		c := do.MustInvoke[*config.Config](i)
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		issuer := do.MustInvoke[*signin.Service](i)
		users := do.MustInvoke[*user.Service](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		service := session.NewService(pool, issuer, users, recorder, log)
		// The inactivity bound reads the catalog at renewal time; a nil
		// settings feature keeps the catalog default, the state a bare
		// wiring is in.
		if settings := do.MustInvoke[*appconfig.Settings](i); settings != nil {
			service.WithSettings(settings)
		}
		// The ban's side effects are wired here rather than in the user
		// provider: the session lifecycle and the queue exist by the time
		// this builds, and the user service must not depend on either to
		// construct. A ban ends the account's live sessions through the
		// service and queues its notices through the notifier.
		users.WithBanSideEffects(service, jobs.NewBanNotifier(
			do.MustInvoke[*queue.Client](i),
			do.MustInvoke[*slog.Logger](i),
			c.Mailer.Notifications.UserBannedNoticeEnabled,
			c.Mailer.Notifications.UserUnbannedNoticeEnabled,
		))
		return service, nil
	}),

	// The credential policy is one provider: the catalog reads and the
	// breach corpus ride together, so every consumer — sign-up, the
	// administrator's create, the recovery flows, the sign-in flag — sees
	// the same rules. The corpus rides the shared outbound client (its
	// circuit breaker is the dead-upstream answer). The range API is
	// unauthenticated, so the account key only becomes a request header
	// when a deployment sets one; the feature's own switch is the
	// `password.reject_compromised` setting.
	do.Lazy(func(i do.Injector) (*password.Validator, error) {
		c := do.MustInvoke[*config.Config](i)
		log := do.MustInvoke[*slog.Logger](i)
		settings := do.MustInvoke[*appconfig.Settings](i)
		fetch := do.MustInvoke[*fetcher.Client](i)
		checker := password.NewBreachChecker(fetch, c.Auth.HIBPAPIKey, c.Fetcher.UserAgent)
		return password.NewValidator(settings, checker, log), nil
	}),

	do.Lazy(func(i do.Injector) (*signup.Service, error) {
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		service := signup.NewService(pool, recorder, log)
		// The sign-up policy reads the catalog at call time; a nil settings
		// feature runs the bare-wiring policy — open mode, no toggles, no
		// gate. The verification code rides the post-construction seam like
		// the settings do: the code's row belongs to the sign-up's
		// transaction, the message to the queue after it commits.
		if settings := do.MustInvoke[*appconfig.Settings](i); settings != nil {
			service.WithSignupSettings(settings)
		}
		if verifier := do.MustInvoke[*verification.Service](i); verifier != nil {
			service.WithVerification(verifier)
		}
		if policy := do.MustInvoke[*password.Validator](i); policy != nil {
			service.WithPasswordPolicy(policy)
		}
		return service, nil
	}),

	do.Lazy(func(i do.Injector) (*blocklist.Service, error) {
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		return blocklist.NewService(pool, recorder, log), nil
	}),

	do.Lazy(func(i do.Injector) (*user.Service, error) {
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		pictures := do.MustInvoke[*storage.Manager](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		service := user.NewService(pool, recorder, log, pictures)
		if policy := do.MustInvoke[*password.Validator](i); policy != nil {
			service.WithPasswordPolicy(policy)
		}
		if settings := do.MustInvoke[*appconfig.Settings](i); settings != nil {
			service.WithSettings(settings)
		}
		return service, nil
	}),

	// The verification service builds over the mailer and the queue the
	// infrastructure registers; the registry's prewarm walk resolves both,
	// so a process that reaches the listener has them.
	do.Lazy(func(i do.Injector) (*verification.Service, error) {
		c := do.MustInvoke[*config.Config](i)
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		mail := do.MustInvoke[*mailer.Service](i)
		client := do.MustInvoke[*queue.Client](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		settings := do.MustInvoke[*appconfig.Settings](i)
		// The change notices ride the deployment's cost decision; the
		// confirm message the request itself sends is transactional and
		// enqueues directly. The change toggle reads the catalog at call
		// time; a nil settings feature keeps the gate closed.
		service := verification.NewService(pool, mail, client, recorder, c.App.BaseURL, log).
			WithEmailChangeNotifier(jobs.NewEmailChangeNotifier(client, log, c.Mailer.Notifications.EmailChangeNoticeEnabled))
		if settings != nil {
			service.WithEmailChangeGate(settings)
		}
		return service, nil
	}),

	// The one-time access service builds over the sign-in issuer — the
	// exchange opens the session through it, so the session rules live in one
	// place — and over the mailer and the queue the email sends run through.
	do.Lazy(func(i do.Injector) (*onetimeaccess.Service, error) {
		c := do.MustInvoke[*config.Config](i)
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		issuer := do.MustInvoke[*signin.Service](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		mail := do.MustInvoke[*mailer.Service](i)
		client := do.MustInvoke[*queue.Client](i)
		return onetimeaccess.NewService(*c, pool, issuer, recorder, mail, client, log), nil
	}),

	// The multifactor service is resolved beside the sign-in issuer it
	// gates: the gate rides the issuer after both exist, which is the same
	// post-construction wiring the ban's side effects run. A provider cannot
	// take the gate as a dependency without the two constructing in a cycle.
	do.Lazy(func(i do.Injector) (*multifactor.Service, error) {
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		issuer := do.MustInvoke[*signin.Service](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		c := do.MustInvoke[*config.Config](i)
		// The cipher seals each enrollment's TOTP secret; it is the auth
		// half — derived from AUTH_SECRET_KEY, not the application secret —
		// because the secret is account-authentication material: rotating
		// the auth secret is what invalidates it, and rotating the
		// application secret never touches a second factor. A test or a
		// consumer area that named no secret key has no sealing to run, and
		// the service answers that state on the first ceremony rather than
		// failing a run that never touches the feature. A real deployment
		// always carries the key — validation demands it — so the production
		// path never sees the nil.
		var cipher *crypto.Cipher
		if c.Auth.SecretKey != "" {
			built, err := crypto.NewAuthCipher(c.Auth.SecretKey)
			if err != nil {
				return nil, fmt.Errorf("identity: multifactor cipher: %w", err)
			}
			cipher = built
		}
		service := multifactor.NewService(pool, cipher, issuer, recorder, "Tango", log)
		// The enrollment ceiling reads the catalog at call time; a nil
		// settings feature keeps the constant, the state a bare wiring is
		// in.
		if settings := do.MustInvoke[*appconfig.Settings](i); settings != nil {
			service.WithEnrollmentSettings(settings)
		}
		issuer.WithMFAGate(service)
		// The notification channel rides the post-construction seam like the
		// gate does: internal/jobs cannot sit below the multifactor package
		// without the cycle the password recovery's enqueuer avoids too.
		client := do.MustInvoke[*queue.Client](i)
		service.WithNoticeEnqueuer(jobs.NewMfaDisabledNotifier(client, log, c.Mailer.Notifications.MfaDisabledNoticeEnabled))
		// The decrypted-secret aid answers the configuration's flag, and the
		// mode gate repeats what validation refuses: the flag outside the
		// development mode is a misconfiguration, so both layers hold even
		// if a caller skips validation.
		service.WithExposedSecrets(c.App.ExposeTotpSecret && c.App.Mode == config.ModeDevelopment)
		return service, nil
	}),

	// The webauthn service builds over the sign-in issuer — the assertion
	// opens the session through it, so the session rules live in one place —
	// and over the settings feature the ceremony knobs read through. The RP
	// identity derives from app.base_url at construction; a configuration
	// without a host fails the run here, before a listener opens.
	do.Lazy(func(i do.Injector) (*webauthn.Service, error) {
		c := do.MustInvoke[*config.Config](i)
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		issuer := do.MustInvoke[*signin.Service](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		// A nil settings feature keeps the fail-closed reads — the state a
		// bare wiring is in; the composition root always resolves the
		// catalog.
		var reader webauthn.SettingsReader
		if settings := do.MustInvoke[*appconfig.Settings](i); settings != nil {
			reader = settings
		}
		service, err := webauthn.NewService(*c, pool, webauthn.NewRepository(), issuer, reader, recorder, log)
		if err != nil {
			return nil, err
		}
		// The email-code proof's send pair rides the post-construction
		// seam, the way the password recovery service takes its notifier:
		// the delivery is optional wiring, and a nil pair leaves the
		// password and passkey proofs working.
		if mail := do.MustInvoke[*mailer.Service](i); mail != nil {
			service.WithDelivery(mail, jobs.NewReauthenticationCodeNotifier(
				do.MustInvoke[*queue.Client](i), log))
		}
		return service, nil
	}),

	// The step-up consumer is the webauthn service behind the interface the
	// guard defines: the transport spends its proofs without learning the
	// feature, and the registry resolves the seam rather than the feature.
	do.Lazy(func(i do.Injector) (guard.ReauthConsumer, error) {
		return do.MustInvoke[*webauthn.Service](i), nil
	}),

	// The password recovery service builds over the mailer; the queue and
	// the session lifecycle ride the post-construction seams, because
	// internal/jobs cannot sit below the password package (the cycle runs
	// jobs → apikey → user → password) and the session service is built
	// beside this one.
	do.Lazy(func(i do.Injector) (*password.Service, error) {
		c := do.MustInvoke[*config.Config](i)
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		mail := do.MustInvoke[*mailer.Service](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		client := do.MustInvoke[*queue.Client](i)
		sessions := do.MustInvoke[*session.Service](i)
		service := password.NewService(pool, mail, recorder, c.App.BaseURL, log).
			WithEnqueuer(jobs.NewPasswordResetNotifier(client, log, c.Mailer.Notifications.PasswordChangedNoticeEnabled)).
			WithSessionEnder(sessions).
			WithUUIDDecoder(user.UUIDFromWire)
		if policy := do.MustInvoke[*password.Validator](i); policy != nil {
			service.WithPasswordPolicy(policy)
		}
		// The add-password procedure's write side is this service's, wired
		// behind the account surface's seam here rather than in the user
		// provider: the user service must not depend on the credential one
		// to construct — session builds over user, and password over
		// session, so wiring it here is what keeps the graph a line.
		if users := do.MustInvoke[*user.Service](i); users != nil {
			users.WithPasswordSetter(service)
		}
		return service, nil
	}),

	do.Lazy(func(i do.Injector) (*usergroup.Service, error) {
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		service := usergroup.NewService(pool, recorder, log)
		// The account views' group seam rides the post-construction wiring
		// like the ban's side effects do: the directory is the group
		// feature's own repository, and the user service must not construct
		// against it to stay free of the import the other way would cycle.
		users := do.MustInvoke[*user.Service](i)
		users.WithGroups(usergroup.NewRepository())
		return service, nil
	}),

	do.Lazy(func(i do.Injector) (*authorization.Service, error) {
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		return authorization.NewService(pool, recorder, log, do.MustInvoke[cache.Cache](i)), nil
	}),

	do.Lazy(func(i do.Injector) (*devicelogin.Service, error) {
		c := do.MustInvoke[*config.Config](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		users := do.MustInvoke[*user.Service](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		return devicelogin.NewService(pool, users, recorder, c.App.BaseURL), nil
	}),

	// The OAuth SSO service builds over the pool and the application
	// cipher — the sealing half: the client secrets and, later, the
	// provider tokens are deployment-configuration material, so rotating
	// AUTH_SECRET_KEY never touches them. A nil cipher (no application
	// secret) is answered at the call site: reads serve, a sealed write
	// is refused. A nil fetcher leaves the discovery validation
	// unavailable the same way.
	do.Lazy(func(i do.Injector) (*oauthsso.Service, error) {
		c := do.MustInvoke[*config.Config](i)
		log := do.MustInvoke[*slog.Logger](i)
		pool := do.MustInvoke[*datastore.Postgres](i)
		recorder := do.MustInvoke[*audit.Recorder](i)
		fetch := do.MustInvoke[*fetcher.Client](i)
		// The cipher seals the client secrets; it is the application
		// half — derived from the application secret, not AUTH_SECRET_KEY
		// — because a connection's credentials are operator
		// configuration, not account-authentication material.
		var cipher *crypto.Cipher
		if c.App.SecretKey != "" {
			built, err := crypto.NewCipherFromHex(c.App.SecretKey)
			if err != nil {
				return nil, fmt.Errorf("identity: oauthsso cipher: %w", err)
			}
			cipher = built
		}
		return oauthsso.NewService(pool, cipher, recorder, oauthsso.FetcherAdapter(fetch), log), nil
	}),
)

// Mount resolves what this area's features need and builds the module the
// router mounts. It is the other half of the seam the composition root uses,
// and it is where a configuration this area cannot work with becomes a failed
// run rather than a 500 on a client's first request.
func Mount(i do.Injector) (kernel.Module, error) {
	// A missing dependency panics here and is turned back into an error by the
	// invocation that reached this provider, so only the validation below is
	// returned by hand.
	keySet := do.MustInvoke[jwtutils.KeyProvider](i)
	c := do.MustInvoke[*config.Config](i)

	// The service is resolved beside the provider it is wrapped in, so a
	// configuration whose key pair cannot be read is reported by Err() rather
	// than left for the first client that fetches the key set. The cache would
	// answer it with an error on the first request instead.
	service := do.MustInvoke[*jwks.Service](i)
	if err := service.Err(); err != nil {
		return nil, err
	}

	return NewModule(Deps{
		KeySet:        keySet,
		SignIn:        do.MustInvoke[*signin.Service](i),
		Sessions:      do.MustInvoke[*session.Service](i),
		Signup:        do.MustInvoke[*signup.Service](i),
		Users:         do.MustInvoke[*user.Service](i),
		Verification:  do.MustInvoke[*verification.Service](i),
		OneTimeAccess: do.MustInvoke[*onetimeaccess.Service](i),
		Multifactor:   do.MustInvoke[*multifactor.Service](i),
		WebAuthn:      do.MustInvoke[*webauthn.Service](i),

		PasswordRecovery: do.MustInvoke[*password.Service](i),

		// The same development-mode gate the multifactor aid keeps: the
		// configuration's validation refuses the flag outside development,
		// and the wiring repeats it so both layers hold.
		ExposeResetToken: c.App.ExposeResetToken && c.App.Mode == config.ModeDevelopment,
		UserGroups:       do.MustInvoke[*usergroup.Service](i),
		Authorization:    do.MustInvoke[*authorization.Service](i),
		Blocklist:        do.MustInvoke[*blocklist.Service](i),
		DeviceLogin:      do.MustInvoke[*devicelogin.Service](i),
		OAuthSSO:         do.MustInvoke[*oauthsso.Service](i),
	}), nil
}

// features is the area's feature list, the one place an identity feature is
// named. A feature that serves a protocol endpoint — the key set, a discovery
// document — is mounted on the router's root; one that serves the application
// API mounts itself under /api.
func features(deps Deps) []kernel.Module {
	modules := []kernel.Module{
		jwks.NewModule(deps.KeySet),
	}
	if deps.SignIn != nil {
		modules = append(modules, signin.NewModule(deps.SignIn))
	}
	if deps.Signup != nil {
		modules = append(modules, signup.NewModule(deps.Signup))
	}
	if deps.Sessions != nil {
		modules = append(modules, session.NewModule(deps.Sessions))
	}
	if deps.Users != nil {
		modules = append(modules, user.NewModule(deps.Users))
	}
	if deps.Verification != nil {
		modules = append(modules, verification.NewModule(deps.Verification))
	}
	if deps.Blocklist != nil {
		// The blocklist's gates ride the post-construction seams: the
		// sign-up, sign-in, and verification features define the
		// interfaces they ask through, and the blocklist service
		// satisfies them here — the feature packages must not import
		// each other.
		if deps.Signup != nil {
			deps.Signup.WithBlocklist(deps.Blocklist)
		}
		if deps.SignIn != nil {
			deps.SignIn.WithBlocklist(deps.Blocklist)
		}
		if deps.Verification != nil {
			deps.Verification.WithSubaddressGuard(deps.Blocklist)
		}
		modules = append(modules, blocklist.NewModule(deps.Blocklist))
	}
	if deps.OneTimeAccess != nil {
		modules = append(modules, onetimeaccess.NewModule(deps.OneTimeAccess))
	}
	if deps.UserGroups != nil {
		modules = append(modules, usergroup.NewModule(deps.UserGroups))
	}
	if deps.Authorization != nil {
		modules = append(modules, authorization.NewModule(deps.Authorization))
	}
	// The second factor's feature is mounted like the rest: a nil service is
	// skipped, and the wiring is the one place the feature is named.
	if deps.Multifactor != nil {
		modules = append(modules, multifactor.NewModule(deps.Multifactor))
	}
	// The passkey feature mounts beside it. The TOTP-count seam rides the
	// post-construction wiring: mfa.max_enrollments counts both factors
	// together, and the multifactor package must not sit below this one.
	// The challenge seam runs the other way for the same reason: the
	// multifactor service verifies the passkey half of its bridge through
	// the interface it defines, satisfied here. The probe page rides the
	// development mode's gate the TOTP aid keeps.
	if deps.WebAuthn != nil {
		if deps.Multifactor != nil {
			deps.WebAuthn.WithTotpEnrollments(deps.Multifactor)
			deps.Multifactor.WithPasskeyVerifier(deps.WebAuthn)
		}
		modules = append(modules, webauthn.NewModule(deps.WebAuthn))
	}
	if deps.PasswordRecovery != nil {
		modules = append(modules, password.NewRecoveryModule(deps.PasswordRecovery).
			WithExposedResetToken(deps.ExposeResetToken))
	}
	if deps.DeviceLogin != nil {
		modules = append(modules, devicelogin.NewModule(deps.DeviceLogin))
	}
	if deps.OAuthSSO != nil {
		modules = append(modules, oauthsso.NewModule(deps.OAuthSSO))
	}
	return modules
}

// resolvedAlgorithmSource names where the signing algorithm came from, for
// the startup log: the configuration when auth.jwt_algorithm is set, the
// material's own kind otherwise. It describes, it does not decide — the
// decision is jwks.Service.SigningAlgorithm's.
func resolvedAlgorithmSource(c *config.Config) string {
	if c.Auth.JWTAlgorithm != "" {
		return "configured"
	}
	if c.Auth.SecretKey != "" {
		return "secret length"
	}
	return "database row"
}
