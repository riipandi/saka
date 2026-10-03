package jwtutils

import (
	"context"
	"fmt"
	"slices"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"

	"github.com/riipandi/saka/internal/authz"
)

// AccessClaims are the private claims an access token carries. The subject is
// the account's ID; every other field is duplicated here so a verifier reads
// the token without a round trip. The type is shared because more than one
// consumer decodes it: the feature that signs and every seam that verifies.
type AccessClaims struct {
	Email       string `json:"email"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	SessionID   string `json:"sid"`

	// Roles are the names of the roles the account holds at signing time,
	// and Permissions the effective grants — the roles' permissions plus
	// the ones granted to the account directly. Both are snapshots: an
	// assignment that changes after this token was signed lands in the
	// next one, which is why the access token's life is short and its
	// renewal re-reads the account.
	//
	// The lists are what the guard judges and what a frontend renders, so
	// they travel with the caller rather than costing a lookup per
	// request. They are omitted from tokens that name neither — the
	// machine credentials and the accounts without any grant — so an
	// absent claim is an empty set, not a missing field.
	Roles       []string `json:"roles,omitzero"`
	Permissions []string `json:"permissions,omitzero"`

	// ActorID and ActorUsername name the account a delegated token acts on
	// behalf of — the administrator who asked for it, when the token was
	// issued to another account. The subject still names the account the
	// request runs as; these two name who is behind it, which is what an
	// audit record and a refusal both need. They are omitted from a token
	// that belongs to the account it names, which is every token that is not
	// a delegation, so a claim that is present is the whole signal that the
	// caller is acting for someone else.
	//
	// The pair is this application's spelling of the delegation an OAuth
	// token exchange (RFC 8693) calls `act`: the actor is recorded beside the
	// subject rather than replacing it, so no seam has to reconstruct who is
	// really acting. ImpersonateUser mints a token with the pair set and
	// StopImpersonating returns the caller to their own account; the session
	// row records the delegation in `impersonated_by`, and the renewal
	// re-signs the pair from that column.
	//
	// The renewal drops ActorUsername today (only ActorID survives), so a
	// delegation's claims lose half the pair mid-life — tracked as a defect.
	ActorID       string `json:"actor_id,omitzero"`
	ActorUsername string `json:"actor_username,omitzero"`
}

// Caller is the authenticated principal a request runs as: the account the
// token names, plus the delegation it may carry.
//
// It is the one type a seam reads from the request context, so a guard, a
// feature, and an audit record agree about who is acting and who is being
// acted for. Reading the raw claims would leave each of them to answer the
// delegation question itself, and the answers would drift.
// BearerScheme is the authorization scheme the access token is presented
// under. It is written once, because the sign-in that answers token_type and
// the renewal that answers it again must spell the same word.
const BearerScheme = "Bearer"

// AdministratorRole is the role name the guard's admin rule reads. It is
// aliased from internal/authz so a caller's question and the policy's answer
// spell it the same way.
const AdministratorRole = authz.AdministratorRole

// CredentialKind names the channel a caller proved itself through. The kind
// is not a claim a token carries — it is how the caller arrived — so it lives
// on the Caller beside the claims rather than inside them.
//
// A machine credential is not a lesser caller: it acts as its owner through
// the same guard table. What the kind exists for is the refusal one surface
// owes every credential that cannot revoke itself: the API keys' own
// management surface is browser-session work, and a key that could manage
// keys could outlive its owner's intent.
type CredentialKind string

const (
	// CredentialSession is a caller who presented an access token the
	// verifier signed. It is the kind every bearer caller carries, and the
	// zero value a caller built without a kind answers: a token is the only
	// credential that exists without this field.
	CredentialSession CredentialKind = "session"

	// CredentialAPIKey is a caller who presented an X-API-Key header the
	// key service validated against its stored hash.
	CredentialAPIKey CredentialKind = "api_key"
)

type Caller struct {
	// AccessClaims describe the account the request acts as, as the token
	// asserted them when it was signed.
	AccessClaims

	// UserID is the account the request acts as: the token's subject. It is
	// separate from the claims because the subject is a registered claim, not
	// a private one, and a request's identity is its subject.
	UserID string

	// Credential names the channel the caller proved itself through. The
	// zero value is a session, because a token is the only credential the
	// field's absence can mean.
	Credential CredentialKind
}

// NewCaller builds the caller from a verified token. A token whose subject is
// absent cannot name the account it acts for, so it is refused rather than
// answered with an empty identity.
func NewCaller(verified Verified[AccessClaims]) (*Caller, error) {
	if verified.Subject == "" {
		return nil, ErrMissingSubject
	}
	return &Caller{AccessClaims: verified.Private, UserID: verified.Subject}, nil
}

// IsImpersonating reports whether the caller acts for another account. A
// procedure that may only ever be used by the account itself refuses an
// impersonated caller on this flag, before it looks at any identifier: a
// delegated session is an administrator's tool, not a way to act as somebody
// else on a surface that belongs to them.
func (c *Caller) IsImpersonating() bool {
	return c != nil && c.ActorID != ""
}

// IsMachine reports whether the caller proved itself through a credential
// that is not a session — today, an API key. The guard's session rule refuses
// such a caller on the surface that manages the credentials themselves: a
// key that could issue and revoke keys would outlive its owner's intent.
func (c *Caller) IsMachine() bool {
	return c != nil && c.Credential == CredentialAPIKey
}

// ActsFor reports whether the caller is the named account. An empty name is
// nobody: a request that names no account cannot match a caller, so a guard
// that reads a missing field refuses rather than passing.
func (c *Caller) ActsFor(userID string) bool {
	return c != nil && userID != "" && c.UserID == userID
}

// HasRole reports whether the caller's token carried the named role. The
// answer is the signing-time snapshot: an assignment made after the token
// was signed is invisible to it until the token is renewed.
func (c *Caller) HasRole(role string) bool {
	return c != nil && slices.Contains(c.Roles, role)
}

// IsAdministrator reports whether the caller holds the administrator role —
// the standing grant the guard's admin rule reads and every administrative
// surface answers to. It is the role claim, not a claim of its own: a token
// that carried `is_admin` would answer a question the role set already
// answers, and the two would drift.
func (c *Caller) IsAdministrator() bool {
	return c.HasRole(AdministratorRole)
}

// HasPermission reports whether the caller's effective grants satisfy the
// requirement — a role's permission or a direct grant, matched with the
// wildcard the slug grammar allows in the instance position.
func (c *Caller) HasPermission(requirement string) bool {
	return c != nil && authz.Grants(c.Permissions, requirement)
}

// SigningKeySource supplies the material the process signs and verifies its
// own access tokens with, across the dual stack: a symmetric algorithm signs
// with the HMAC secret, anything else with the configured key pair.
//
// jwks.Service satisfies it. The cached KeyProvider does not — an HMAC secret
// is not in the published set, so a verifier resolves through the key service
// itself and a rotation is picked up on the next call.
type SigningKeySource interface {
	KeyProvider
	HMACKey(ctx context.Context) (jwk.Key, error)
	SigningAlgorithm() (jwa.SignatureAlgorithm, error)
}

// AccessVerifier verifies the access tokens this process signs. The key and
// algorithm resolve on every call, and the verifier is built with that exact
// pair, so the accepted algorithm is pinned by construction — a token signed
// under another algorithm fails before any key material is tried.
type AccessVerifier struct {
	keys   SigningKeySource
	issuer string
}

// NewAccessVerifier builds the verifier over the key service and the issuer
// the deployment signs with.
func NewAccessVerifier(keys SigningKeySource, issuer string) *AccessVerifier {
	return &AccessVerifier{keys: keys, issuer: issuer}
}

// Verify checks the token and decodes its typed private claims.
func (v *AccessVerifier) Verify(ctx context.Context, token string) (Verified[AccessClaims], error) {
	algorithm, err := v.keys.SigningAlgorithm()
	if err != nil {
		return Verified[AccessClaims]{}, fmt.Errorf("jwtutils: signing algorithm: %w", err)
	}

	var key jwk.Key
	if algorithm.IsSymmetric() {
		key, err = v.keys.HMACKey(ctx)
	} else {
		key, err = v.keys.SignKey(ctx)
	}
	if err != nil {
		return Verified[AccessClaims]{}, fmt.Errorf("jwtutils: signing key: %w", err)
	}

	verifier, err := NewVerifier[AccessClaims](key, algorithm)
	if err != nil {
		return Verified[AccessClaims]{}, fmt.Errorf("jwtutils: verifier: %w", err)
	}
	return verifier.WithIssuer(v.issuer).Verify(token)
}

// VerifyCaller verifies the token and answers the principal it authenticates.
//
// It is the door every authenticated surface uses: the caller carries the
// subject beside the claims, so a guard compares identifiers instead of
// reaching into two shapes, and the delegation the token may carry travels
// with it. A token that names no subject is refused here rather than handed on
// as an identity nobody can check.
func (v *AccessVerifier) VerifyCaller(ctx context.Context, token string) (*Caller, error) {
	verified, err := v.Verify(ctx, token)
	if err != nil {
		return nil, err
	}
	return NewCaller(verified)
}
