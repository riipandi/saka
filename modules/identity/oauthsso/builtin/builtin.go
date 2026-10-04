// Package builtin holds the provider definitions the code ships: the
// endpoints, the default scopes, and the identity source of the providers
// a deployment signs accounts in with out of the box. A builtin connection
// row references one of these by slug; the endpoints are never stored,
// because a stored endpoint would let a database write redirect the flow
// to a host the code never chose.
//
// Google speaks OIDC: the flow verifies the id_token the token endpoint
// returned. GitHub speaks plain OAuth2: the identity is read from its
// user API, and the primary verified email is what the linking rule
// judges.
package builtin

// Definition is one builtin provider's fixed shape.
type Definition struct {
	// Slug is the word a connection row names and BeginSignIn asks for.
	Slug string
	// DisplayName is the default name a connection renders before the
	// operator rewrites it.
	DisplayName string
	// Scopes are the default scopes the authorize request asks for.
	Scopes []string
	// OIDC marks a provider whose id_token the flow verifies; a false
	// value is an OAuth2-only provider whose identity comes from
	// UserinfoURL.
	OIDC bool
	// Issuer is the identifier the verified id_token's iss claim must
	// answer. An OAuth2-only provider carries none.
	Issuer string
	// AuthorizationURL and TokenURL are the provider's fixed endpoints.
	AuthorizationURL string
	TokenURL         string
	// JwksURL is the key set the id_token is verified against.
	JwksURL string
	// UserinfoURL is the identity endpoint an OAuth2-only provider
	// serves; empty for an OIDC provider, whose id_token carries the
	// claims.
	UserinfoURL string
	// EmailsURL is the address-list endpoint a provider serves beside
	// its user API, where the verified flag lives. Empty for a provider
	// whose id_token answers it.
	EmailsURL string
}

// definitions indexes the shipped providers by slug.
var definitions = map[string]Definition{
	Google.Slug: Google,
	GitHub.Slug: GitHub,
}

// BySlug answers the builtin definition the slug names. A slug the
// package does not ship names no builtin connection — the second word a
// custom connection's slug must avoid.
func BySlug(slug string) (Definition, bool) {
	def, ok := definitions[slug]
	return def, ok
}

// Slugs answers the shipped providers' slugs.
func Slugs() []string {
	out := make([]string, 0, len(definitions))
	for slug := range definitions {
		out = append(out, slug)
	}
	return out
}
