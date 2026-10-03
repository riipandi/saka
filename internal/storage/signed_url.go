package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/riipandi/saka/pkg/crypto"
)

// linkPathPrefix is the mount the signed links compose against: the engine
// names the surface its links answer on, and the transport mounts the
// handler there.
const linkPathPrefix = "/storage"

// SignedURLTTLFunc is the expiry source a SignedURL without an explicit
// ttl reads. The registry wires it over the `storage.signed_url_expires`
// appconfig setting, so an operator's change takes effect at the next
// minted link.
type SignedURLTTLFunc func(ctx context.Context) (time.Duration, error)

// linkDomain separates the signed-link payload from any other HMAC use of
// the same secret: a signature over one purpose never verifies as another.
const linkDomain = "saka-storage-signed-link"

// Signer mints and verifies the signed links a private object is read
// over. The link is the query string `?exp=<unix>&sig=<hex>`: the expiry is
// inside the signed payload, so a truncated lifetime cannot be extended by
// editing the parameter, and the verification compares in constant time.
// The key is the application secret (app.secret_key) through the HMAC key
// parse — no second key format exists. Rotating the secret invalidates the
// links already issued; a rotation is a rare, deliberate act.
type Signer struct {
	key []byte
	// now is the clock the expiry compares against; tests move it.
	now func() time.Time
}

// NewSigner builds the signer over the application secret's hex form.
func NewSigner(secretHex string) (*Signer, error) {
	key, err := crypto.ParseHMACKeyHex(secretHex)
	if err != nil {
		return nil, fmt.Errorf("storage: signed links: %w", err)
	}
	return &Signer{key: key, now: time.Now}, nil
}

// Sign answers the signature over the bucket/key pair with the expiry
// sealed inside.
func (s *Signer) Sign(bucket, key string, exp time.Time) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(linkPayload(bucket, key, exp.Unix())))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify answers whether the signature a link carries names the bucket/key
// pair, was minted with the same secret, and had not expired when the
// verification ran. Every failure — tampered parameter, wrong secret,
// elapsed expiry — is the same false, and the comparison is constant time.
func (s *Signer) Verify(bucket, key string, exp int64, sig string) bool {
	if exp <= s.now().Unix() || sig == "" {
		return false
	}
	expected := s.Sign(bucket, key, time.Unix(exp, 0))
	return hmac.Equal([]byte(expected), []byte(sig))
}

// SignedURL composes the full link: `{base}/{bucket}/{key}?exp=…&sig=…`.
// base is the public origin the assets are served from (app.assets_url),
// the `/storage` prefix included; its query, if any, is preserved and the
// signed parameters ride beside it.
func (s *Signer) SignedURL(base, bucket, key string, exp time.Time) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("storage: signed link base %q: %w", base, err)
	}
	if parsed.Path == "" {
		parsed.Path = linkPathPrefix
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/" + bucket + "/" + key

	query := parsed.Query()
	query.Set("exp", strconv.FormatInt(exp.Unix(), 10))
	query.Set("sig", s.Sign(bucket, key, exp))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// linkPayload is the exact byte string a signature covers.
func linkPayload(bucket, key string, exp int64) string {
	return linkDomain + "\n" + bucket + "\n" + key + "\n" + strconv.FormatInt(exp, 10)
}
