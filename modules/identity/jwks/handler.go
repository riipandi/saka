package jwks

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/lestrrat-go/jwx/v3/jwk"

	"github.com/riipandi/saka/framework/webutil"
	"github.com/riipandi/saka/pkg/jwtutils"
)

// Path is where the key set is published. It is the standard location a
// client reads before it verifies a token, and it sits in the `/.well-known`
// namespace the transport already reserves.
const Path = "/.well-known/jwks.json"

// ModuleName is the name this feature reports under. The area it belongs to
// qualifies it, so the name is the feature alone.
const ModuleName = "jwks"

// cacheControl is how long a client may reuse a fetched set. A key set
// changes on a rotation, which is rare and announced by a new `kid`; an hour
// keeps a busy client from re-fetching on every verification while still
// picking up a rotation the same day.
const cacheControl = "public, max-age=3600"

// Module mounts the key set endpoint.
type Module struct {
	keys jwtutils.KeyProvider
}

// NewModule builds the module over a key provider. The provider is the
// interface rather than the service, so a cache in front of the service is
// invisible here.
func NewModule(keys jwtutils.KeyProvider) *Module {
	return &Module{keys: keys}
}

// Name reports the module in composition reports.
func (m *Module) Name() string { return ModuleName }

// Mount registers the endpoint. It runs once, before the listener opens.
func (m *Module) Mount(r chi.Router) {
	r.Get(Path, m.handle)
}

// handle answers the key set.
//
// The response is a bare JWK Set, not the API envelope: RFC 7517 defines the
// document a client parses, and wrapping it would make the endpoint
// unreadable to every standard library that fetches a JWKS. A failure still
// goes through the envelope, and the request id travels in its header either
// way, so a failure can be correlated with the log.
func (m *Module) handle(w http.ResponseWriter, r *http.Request) {
	if m.keys == nil {
		// An area mounted without its dependency is a wiring defect, and the
		// registry fails the run before this is reachable. Answering an empty
		// set is the safe response if it ever is: a client must not be told
		// that every key is valid.
		webutil.Fail(w, r, http.StatusInternalServerError, "key set unavailable")
		return
	}

	set, err := m.keys.VerifyKeySet(r.Context())
	if err != nil {
		// The configured key is unreadable, which is a broken deployment
		// rather than a bad request. The error text is not published: it
		// names a configuration key.
		webutil.Fail(w, r, http.StatusInternalServerError, "key set unavailable")
		return
	}

	w.Header().Set("Cache-Control", cacheControl)
	webutil.WriteJSON(w, http.StatusOK, renderSet(set))
}

// setDocument is the wire form: the `keys` array RFC 7517 names.
type setDocument struct {
	Keys []jsontext.Value `json:"keys"`
}

// renderSet renders the set the way a client expects to read it.
//
// jwx marshals each key itself, so the endpoint never restates a key's
// fields. A key that cannot be marshalled is left out rather than failing the
// document: the set is still usable for every other key.
func renderSet(set jwk.Set) setDocument {
	keys := make([]jsontext.Value, 0, set.Len())
	for i := range set.Len() {
		key, ok := set.Key(i)
		if !ok {
			continue
		}
		encoded, err := json.Marshal(key)
		if err != nil {
			continue
		}
		keys = append(keys, encoded)
	}
	return setDocument{Keys: keys}
}
