package oauthsso

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/riipandi/saka/internal/audit"
	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/identity/blocklist"
	"github.com/riipandi/saka/modules/identity/signin"
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/crypto"
)

// The resolution's settings keys — the catalog's own words, read at call
// time so an operator's change lands on the next flow without a restart.
const (
	SettingAccessMode             = "access.mode"
	SettingAccessAllowlistEnabled = "access.allowlist_enabled"
	SettingAccessAllowlist        = "access.allowlist"
	SettingAccessBlocklistEnabled = "access.blocklist_enabled"
	SettingAccessBlocklist        = "access.blocklist"
	// SettingAccountLinkingEnabled is the feature's own key: the gate the
	// email-match branch reads before it binds a provider identity to the
	// account the address already names.
	SettingAccountLinkingEnabled = "oauthsso.account_linking_enabled"
)

// The email code's shape: the twelve-character unambiguous alphabet the
// verification codes keep, and a window as long as the flow's own — a
// code that outlives its ceremony is a hash on a dead row. The drawn
// form always fits the users column's username check, so the pattern
// lives in the candidate derivation below.
const (
	signInCodeLength   = 12
	signInCodeLifetime = flowLifetime
)

// usernameCandidate derives the JIT username from the address's local
// part, clipped and padded to the users column's check — 3-32 ASCII
// letters, digits, and underscores — with a numeric suffix once the
// plain name is taken.
func usernameCandidate(email string, attempt int) string {
	local := email
	if at := strings.IndexByte(email, '@'); at > 0 {
		local = email[:at]
	}
	var cleaned strings.Builder
	for _, r := range strings.ToLower(local) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			cleaned.WriteRune(r)
		}
	}
	name := cleaned.String()
	if name == "" {
		name = "user"
	}
	if len(name) > 29 {
		name = name[:29]
	}
	if attempt > 0 {
		name = fmt.Sprintf("%s_%d", name, attempt)
	}
	for len(name) < 3 {
		name += "_"
	}
	return name
}

// The failures the resolution reports. The handler maps them onto the
// connect codes; the browser paths never see them.
var (
	// ErrNamesRequired is a continue at the require_names stage whose
	// names arrived empty — or an empty answer on a stage that names
	// none.
	ErrNamesRequired = errors.New("oauthsso: the flow waits for a given and a family name")

	// ErrInvalidCode is a wrong answer at the verify_email stage. The
	// three-strikes counter is the flow's own memory; this error says
	// this answer was wrong, not that the flow died.
	ErrInvalidCode = errors.New("oauthsso: the code does not answer the flow's request")

	// ErrFlowEnded is a verify_email flow that answered wrong three
	// times: the ceremony is closed and every later attempt is unknown.
	ErrFlowEnded = errors.New("oauthsso: the flow answered wrong too many times")

	// ErrLinkingDisabled is a verified provider address that matches an
	// account while the deployment turned account linking off. The
	// address alone is the credential the binding would rest on, so the
	// match signs nothing in.
	ErrLinkingDisabled = errors.New("oauthsso: account linking is disabled")

	// ErrSignUpRefused is a JIT creation the access mode or the
	// identifier gates refused — the sign-up feature's refusal, carried
	// over so both doors answer alike.
	ErrSignUpRefused = errors.New("oauthsso: the sign-up is not allowed")
)

// signInIssuer is the session mint the resolution binds the account
// through. The sign-in service satisfies it; the interface keeps this
// feature from importing it beyond the types the answer needs.
type signInIssuer interface {
	// FindAccountByIDAny reads the account a row names, disabled and
	// banned included — the issuer itself is what refuses those.
	FindAccountByIDAny(ctx context.Context, id uuid.UUID) (*signin.Account, error)
	// IssueSession opens the session. The provider and the audit event
	// are the caller's vocabulary: this feature names its own.
	IssueSession(ctx context.Context, db datastore.Querier, account *signin.Account, provider, event string, params signin.SessionParams) (signin.Result, error)
}

// mfaGate is the second factor's seam, the shape the password path asks:
// whether the account keeps a confirmed authenticator, and the bridge a
// proven first factor mints. The multifactor service satisfies it.
type mfaGate interface {
	GateSignIn(ctx context.Context, userID uuid.UUID, remember bool) (signin.PendingSignIn, error)
	KeepsConfirmedFactor(ctx context.Context, userID uuid.UUID) (bool, error)
}

// settingsReader is the policy's runtime source: the access mode, the
// identifier gates, and the linking toggle.
type settingsReader interface {
	GetString(ctx context.Context, key string) (string, error)
	GetBool(ctx context.Context, key string) (bool, error)
}

// blocklistChecker is the blocked-identifier gate the JIT creation runs.
// The blocklist service satisfies it.
type blocklistChecker interface {
	Blocked(ctx context.Context, address string) (bool, error)
}

// CodeNotifier is the email code's delivery seam: the raw value leaves
// through the queue, the flow row keeps only the hash.
type CodeNotifier interface {
	DeliverSignInCode(ctx context.Context, email, providerName, code string, ttl time.Duration) error
}

// WithIssuer wires the session mint. Nil leaves every continue refused
// — the state a bare wiring is in.
func (s *Service) WithIssuer(issuer signInIssuer) *Service {
	s.issuer = issuer
	return s
}

// WithMFAGate wires the second factor's fork. Nil keeps the fork closed:
// a flow never pauses for a challenge it cannot mint.
func (s *Service) WithMFAGate(gate mfaGate) *Service {
	s.mfa = gate
	return s
}

// WithSettings wires the policy's runtime source. Nil refuses the JIT
// creation — an unreadable access mode is a closed door, the same answer
// the sign-up feature gives.
func (s *Service) WithSettings(settings settingsReader) *Service {
	s.settings = settings
	return s
}

// WithBlocklist wires the identifier gate. Nil lets the creation pass —
// a broken read must never lock the deployment out of its own door.
func (s *Service) WithBlocklist(checker blocklistChecker) *Service {
	s.blocklist = checker
	return s
}

// WithCodeNotifier wires the email code's delivery. Nil leaves the
// verify_email stage unreachable: the flow answers the stage, the code
// goes nowhere, and the account it would have proven never binds.
func (s *Service) WithCodeNotifier(notifier CodeNotifier) *Service {
	s.codes = notifier
	return s
}

// ContinueParams carries one continue call: the flow's handle, the names
// the require_names stage collects, and the client facts the session
// row records.
type ContinueParams struct {
	FlowToken   string
	GivenName   string
	FamilyName  string
	UserAgent   string
	IPAddress   string
	Fingerprint string
}

// ContinueResult is what a continue answers with. Exactly one of the
// three carries: Session on a completed sign-in, Bridge when the
// account keeps a confirmed second factor, Stage when the flow moved to
// a stage the client must serve first.
type ContinueResult struct {
	Session *signin.Result
	Bridge  *signin.PendingSignIn
	Stage   FlowStage
}

// ContinueSignIn runs the resolution over a paused flow: the account the
// resolved identity binds to is picked by the settled order — the
// provider account's binding first, then a verified address match, then
// the JIT creation the open access mode answers for. A verified address
// that matches no account and no binding creates the account with no
// password; a binding's account opens the session through the issuer.
// The MFA fork runs either way: a confirmed second factor answers with
// the bridge, never a session.
func (s *Service) ContinueSignIn(ctx context.Context, params ContinueParams) (ContinueResult, error) {
	if s.issuer == nil {
		return ContinueResult{}, fmt.Errorf("oauthsso: no session issuer is wired")
	}
	flow, err := s.FlowByToken(ctx, params.FlowToken, StageResolved, StageRequireNames)
	if err != nil {
		return ContinueResult{}, err
	}
	conn, err := s.repo.ByID(ctx, s.pool, flow.ConnectionID)
	if err != nil {
		return ContinueResult{}, err
	}
	if !conn.Enabled {
		return ContinueResult{}, ErrConnectionUnavailable
	}

	if flow.Stage == StageRequireNames {
		if strings.TrimSpace(params.GivenName) == "" || strings.TrimSpace(params.FamilyName) == "" {
			return ContinueResult{}, ErrNamesRequired
		}
		stored, storedErr := s.repo.UpdateFlowNames(ctx, s.pool, flow.ID,
			strings.TrimSpace(params.GivenName), strings.TrimSpace(params.FamilyName))
		if storedErr != nil {
			return ContinueResult{}, storedErr
		}
		if !stored {
			return ContinueResult{}, ErrFlowUnknown
		}
		flow.GivenName = strings.TrimSpace(params.GivenName)
		flow.FamilyName = strings.TrimSpace(params.FamilyName)
	}

	// The binding question first: the provider identity this flow
	// resolved may already ride an account.
	binding, err := s.repo.LinkedAccountByProvider(ctx, s.pool, flow.ConnectionID, flow.ProviderAccountID)
	switch {
	case err == nil:
		return s.openSession(ctx, flow, conn, binding.UserID, params)
	case errors.Is(err, datastore.ErrNoRows):
		// The branches below decide.
	default:
		return ContinueResult{}, err
	}

	// The verified address is the linking credential. An unverified one
	// proves nothing yet — the email code must — so it never matches an
	// account, however well the address names one.
	if flow.EmailVerified {
		accountID, accountErr := s.repo.AccountIDByEmail(ctx, s.pool, flow.Email)
		switch {
		case accountErr == nil:
			if linking, err := s.linkingEnabled(ctx); err != nil {
				return ContinueResult{}, err
			} else if !linking {
				return ContinueResult{}, ErrLinkingDisabled
			}
			return s.linkAndOpen(ctx, flow, conn, accountID, params)
		case errors.Is(accountErr, datastore.ErrNoRows):
			// No account carries the address: the JIT branch decides.
		default:
			return ContinueResult{}, accountErr
		}
		return s.provisionJIT(ctx, flow, conn, params)
	}

	// The provider's address carries no verified mark: the flow pauses
	// for the email code, and nothing binds until it is spent.
	return s.pauseForEmailCode(ctx, flow, conn)
}

// VerifySignInEmail spends the code a verify_email flow waits for. The
// spend is single-use and carried in one guarded update — wrong hash,
// spent stage, and the three-strikes ceiling all answer "not spent" —
// and the winner re-runs the resolution's questions with the address
// now proven: the answer names the stage the flow moved to, and the
// sign-in itself completes through ContinueSignIn.
func (s *Service) VerifySignInEmail(ctx context.Context, flowToken, code string) (FlowStage, error) {
	flow, err := s.FlowByToken(ctx, flowToken, StageVerifyEmail)
	if err != nil {
		return "", err
	}
	codeHash := hashSecret(strings.TrimSpace(code))
	next := StageResolved
	if strings.TrimSpace(flow.GivenName) == "" || strings.TrimSpace(flow.FamilyName) == "" {
		next = StageRequireNames
	}
	spent, err := s.repo.SpendEmailCode(ctx, s.pool, flow.ID, codeHash, next)
	if err != nil {
		return "", err
	}
	if !spent {
		dead, err := s.repo.StrikeEmailCode(ctx, s.pool, flow.ID, flow.WrongCodes)
		if err != nil {
			return "", err
		}
		if dead {
			if err := s.repo.FailFlow(ctx, s.pool, flow.ID); err != nil {
				return "", err
			}
			return "", ErrFlowEnded
		}
		return "", ErrInvalidCode
	}
	return next, nil
}

// pauseForEmailCode moves the flow to the verify_email stage and sends
// the code. The row's flip is the guarded write; the message leaves
// after it commits, and a delivery that cannot run — no notifier wired,
// no mailer configured — fails the continue, not the flow.
func (s *Service) pauseForEmailCode(ctx context.Context, flow Flow, conn Connection) (ContinueResult, error) {
	if s.codes == nil {
		return ContinueResult{}, fmt.Errorf("oauthsso: no code notifier is wired")
	}
	code, err := crypto.RandomString(signInCodeLength, crypto.AlphabetUnambiguous)
	if err != nil {
		return ContinueResult{}, fmt.Errorf("oauthsso: draw code: %w", err)
	}
	moved, err := s.repo.MoveToVerifyEmail(ctx, s.pool, flow.ID, hashSecret(code), s.now().Add(signInCodeLifetime))
	if err != nil {
		return ContinueResult{}, err
	}
	if !moved {
		return ContinueResult{}, ErrFlowUnknown
	}
	if err := s.codes.DeliverSignInCode(ctx, flow.Email, conn.DisplayName, code, signInCodeLifetime); err != nil {
		return ContinueResult{}, err
	}
	return ContinueResult{Stage: StageVerifyEmail}, nil
}

// linkAndOpen binds the provider identity to the account the verified
// address already names, then opens the session — one transaction, so
// the binding and the session it earned commit together.
func (s *Service) linkAndOpen(ctx context.Context, flow Flow, conn Connection, accountID uuid.UUID, params ContinueParams) (ContinueResult, error) {
	account, err := s.issuer.FindAccountByIDAny(ctx, accountID)
	if err != nil {
		return ContinueResult{}, err
	}
	var result ContinueResult
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		if bindErr := s.bindIdentity(ctx, tx, flow, conn, account.ID); bindErr != nil {
			return bindErr
		}
		var openErr error
		result, openErr = s.finish(ctx, tx, flow, account, conn, params)
		return openErr
	})
	if err != nil {
		return ContinueResult{}, err
	}
	return result, nil
}

// provisionJIT creates the account the verified address names and binds
// the identity to it. The gates run cheapest-first — the access mode,
// the allowlist, the blocklist — and the whole ceremony commits as one
// transaction: the account, the binding, and the session it opens.
func (s *Service) provisionJIT(ctx context.Context, flow Flow, conn Connection, params ContinueParams) (ContinueResult, error) {
	allowed, err := s.jitAllowed(ctx, flow.Email)
	if err != nil {
		return ContinueResult{}, err
	}
	if !allowed {
		return ContinueResult{}, ErrSignUpRefused
	}
	if strings.TrimSpace(flow.GivenName) == "" || strings.TrimSpace(flow.FamilyName) == "" {
		moved, movedErr := s.repo.MoveToRequireNames(ctx, s.pool, flow.ID)
		if movedErr != nil {
			return ContinueResult{}, movedErr
		}
		if !moved {
			return ContinueResult{}, ErrFlowUnknown
		}
		return ContinueResult{Stage: StageRequireNames}, nil
	}

	var result ContinueResult
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		userID, username, createErr := s.createJITAccount(ctx, tx, flow)
		if createErr != nil {
			return createErr
		}
		s.audit.Record(ctx, tx, audit.Entry{
			Event:  audit.EventOauthSsoAccountCreated,
			Status: audit.StatusSuccess,
			UserID: userID.String(),
			Payload: map[string]string{
				"provider": conn.Provider,
				"email":    flow.Email,
			},
		})
		if bindErr := s.bindIdentity(ctx, tx, flow, conn, userID); bindErr != nil {
			return bindErr
		}
		// The account is the row this transaction just wrote: the read
		// back would go through the pool and miss it until the commit,
		// so the session issuer is handed the shape the insert carried.
		account := &signin.Account{
			ID:          userID,
			Username:    username,
			Email:       flow.Email,
			DisplayName: displayName(flow.GivenName, flow.FamilyName),
		}
		var openErr error
		result, openErr = s.finish(ctx, tx, flow, account, conn, params)
		return openErr
	})
	if err != nil {
		return ContinueResult{}, err
	}
	return result, nil
}

// jitAllowed answers whether the address may create an account: the
// access mode must read open — an unreadable source is a closed door,
// the sign-up feature's own answer — and the allowlist and the
// blocklist gate the identity when they are armed. The allowlist wins
// the two lists' conflict, the way the sign-up keeps it.
func (s *Service) jitAllowed(ctx context.Context, email string) (bool, error) {
	if s.settings == nil {
		return false, nil
	}
	mode, err := s.settings.GetString(ctx, SettingAccessMode)
	if err != nil {
		s.log.WarnContext(ctx, "oauthsso: access.mode unreadable; refusing the JIT creation", "error", err)
		return false, nil
	}
	if mode == "invite" {
		return false, nil
	}
	allowlistOn, err := s.settings.GetBool(ctx, SettingAccessAllowlistEnabled)
	if err != nil {
		s.log.WarnContext(ctx, "oauthsso: access.allowlist_enabled unreadable; treating as off", "error", err)
	}
	if allowlistOn {
		list, listErr := s.settings.GetString(ctx, SettingAccessAllowlist)
		if listErr != nil {
			s.log.WarnContext(ctx, "oauthsso: access.allowlist unreadable; treating as empty", "error", listErr)
		}
		accepted := blocklist.Allowlisted(email, blocklist.SplitAllowlist(list))
		if !accepted {
			return false, nil
		}
		return true, nil
	}
	blocklistOn, err := s.settings.GetBool(ctx, SettingAccessBlocklistEnabled)
	if err != nil {
		s.log.WarnContext(ctx, "oauthsso: access.blocklist_enabled unreadable; treating as off", "error", err)
	}
	if !blocklistOn {
		return true, nil
	}
	if s.blocklist == nil {
		return true, nil
	}
	blocked, err := s.blocklist.Blocked(ctx, email)
	if err != nil {
		// A broken read must never lock the deployment out of its own
		// door: the sign-up's rule, kept here.
		s.log.WarnContext(ctx, "oauthsso: the blocklist is unreadable; letting the creation pass", "error", err)
		return true, nil
	}
	return !blocked, nil
}

// linkingEnabled reads the feature's toggle. An unreadable source keeps
// the default — linking on — the catalog's own default, and the state a
// bare wiring runs in.
func (s *Service) linkingEnabled(ctx context.Context) (bool, error) {
	if s.settings == nil {
		return true, nil
	}
	on, err := s.settings.GetBool(ctx, SettingAccountLinkingEnabled)
	if err != nil {
		s.log.WarnContext(ctx, "oauthsso: the linking toggle is unreadable; using the default", "error", err)
		return true, nil
	}
	return on, nil
}

// createJITAccount writes the account row: the address, the names, and
// the verified stamp — the provider's mark or the spent code, the row
// already carries which. The username derives from the address's local
// part, and a taken name retries with a suffix: a JIT creation must not
// fail because an earlier account claimed the same local part.
func (s *Service) createJITAccount(ctx context.Context, tx datastore.Querier, flow Flow) (uuid.UUID, string, error) {
	name := displayName(flow.GivenName, flow.FamilyName)
	verifiedAt := s.now()
	attempt := usernameCandidate(flow.Email, 0)
	for tries := 0; tries < 3; tries++ {
		userID, err := s.repo.CreateAccount(ctx, tx, user.UserSchema{
			Username:        attempt,
			Email:           flow.Email,
			FirstName:       flow.GivenName,
			LastName:        flow.FamilyName,
			DisplayName:     name,
			EmailVerifiedAt: &verifiedAt,
		})
		if err == nil {
			return userID, attempt, nil
		}
		if !uniqueViolation(err) {
			return uuid.Nil(), "", fmt.Errorf("oauthsso: create account: %w", err)
		}
		attempt = usernameCandidate(flow.Email, tries+1)
	}
	return uuid.Nil(), "", fmt.Errorf("oauthsso: create account: every derived username is taken")
}

// displayName joins the names the provider answered the way the account
// view renders them, with the address's local part as the fallback.
func displayName(given, family string) string {
	switch {
	case given != "" && family != "":
		return given + " " + family
	case given != "":
		return given
	case family != "":
		return family
	default:
		return ""
	}
}

// bindIdentity writes the binding and its audit record inside the
// caller's transaction. The tokens the flow carried move sealed — the
// binding is the row that owns them from now on.
func (s *Service) bindIdentity(ctx context.Context, tx datastore.Querier, flow Flow, conn Connection, userID uuid.UUID) error {
	if err := s.repo.CreateLinkedAccount(ctx, tx, LinkedAccount{
		UserID:            userID,
		ConnectionID:      conn.ID,
		ProviderAccountID: flow.ProviderAccountID,
		Email:             flow.Email,
		EmailVerified:     flow.EmailVerified,
		Profile:           flow.Profile,
		AccessToken:       flow.AccessToken,
		RefreshToken:      flow.RefreshToken,
	}); err != nil {
		return fmt.Errorf("oauthsso: bind identity: %w", err)
	}
	s.audit.Record(ctx, tx, audit.Entry{
		Event:  audit.EventOauthSsoAccountLinked,
		Status: audit.StatusSuccess,
		UserID: userID.String(),
		Payload: map[string]string{
			"provider":            conn.Provider,
			"provider_account_id": flow.ProviderAccountID,
			"oauth_connection_id": wireConnectionID(conn.ID),
		},
	})
	return nil
}

// wireConnectionID renders the connection's identifier in its wire form
// for the audit payload. A render that fails carries the raw UUID — the
// record must not fail the write it rides.
func wireConnectionID(id uuid.UUID) string {
	wire, err := IDFromUUID(id)
	if err != nil {
		return id.String()
	}
	return wire.String()
}

// finish answers the session or the bridge and spends the flow. The
// guarded stage update is the race's referee: a second continue that
// lost the row turns into the unknown-flow answer, whatever its read
// saw a moment before.
func (s *Service) finish(ctx context.Context, tx datastore.Querier, flow Flow, account *signin.Account, conn Connection, params ContinueParams) (ContinueResult, error) {
	if s.mfa != nil {
		keeps, err := s.mfa.KeepsConfirmedFactor(ctx, account.ID)
		if err != nil {
			return ContinueResult{}, err
		}
		if keeps {
			bridge, err := s.mfa.GateSignIn(ctx, account.ID, false)
			if err != nil {
				return ContinueResult{}, err
			}
			spent, err := s.repo.CompleteFlow(ctx, tx, flow.ID, account.ID)
			if err != nil {
				return ContinueResult{}, err
			}
			if !spent {
				return ContinueResult{}, ErrFlowUnknown
			}
			return ContinueResult{Bridge: &bridge, Stage: StageCompleted}, nil
		}
	}
	result, err := s.issuer.IssueSession(ctx, tx, account, signin.ProviderOAuthSSO, audit.EventOauthSsoSignIn, signin.SessionParams{
		UserAgent:   params.UserAgent,
		IPAddress:   params.IPAddress,
		Fingerprint: params.Fingerprint,
	})
	if err != nil {
		return ContinueResult{}, err
	}
	spent, err := s.repo.CompleteFlow(ctx, tx, flow.ID, account.ID)
	if err != nil {
		return ContinueResult{}, err
	}
	if !spent {
		return ContinueResult{}, ErrFlowUnknown
	}
	return ContinueResult{Session: &result, Stage: StageCompleted}, nil
}

// openSession runs the MFA fork and the mint for an account the
// resolution already bound — the first question's branch, and the only
// one that needs no writes beside the flow's.
func (s *Service) openSession(ctx context.Context, flow Flow, conn Connection, userID uuid.UUID, params ContinueParams) (ContinueResult, error) {
	account, err := s.issuer.FindAccountByIDAny(ctx, userID)
	if err != nil {
		return ContinueResult{}, err
	}
	var result ContinueResult
	err = s.pool.WithTx(ctx, func(ctx context.Context, tx datastore.Querier) error {
		var openErr error
		result, openErr = s.finish(ctx, tx, flow, account, conn, params)
		return openErr
	})
	if err != nil {
		return ContinueResult{}, err
	}
	return result, nil
}

// uniqueViolation reports a PostgreSQL unique-constraint failure — the
// users table's uniqueness is what answers the JIT race.
func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
