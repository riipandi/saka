package webauthn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/require"
)

// The soft authenticator: a real WebAuthn authenticator in Go. It holds one
// ECDSA key pair per credential, answers the ceremonies the server opens,
// and reports the flags and counter a test asks for — everything the
// protocol requires, nothing a browser adds. A test that forges with it
// exercises the same verification a real device's answer walks.

// Authenticator flags. UP is always set — an assertion without user presence
// is no assertion; UV answers the verification requirement; BE and BS are
// the backup flags the synced-passkey policy judges; AT marks the attested
// credential data a registration's authenticator data carries.
const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
)

// SoftCredential is one credential the soft authenticator holds.
type SoftCredential struct {
	ID     []byte
	Key    *ecdsa.PrivateKey
	AAGUID [16]byte
}

// SoftAuthenticator is the test's device.
type SoftAuthenticator struct {
	credential *SoftCredential
	// counter is the signature counter the next assertion reports.
	counter uint32
	// backupEligible and backupState are the flags assertions report.
	backupEligible bool
	backupState    bool
	// userVerification is whether assertions carry the UV flag.
	userVerification bool
}

// NewSoftAuthenticator builds a device holding one fresh credential.
func NewSoftAuthenticator(backupEligible, backupState, userVerification bool) *SoftAuthenticator {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic("softauthn: key: " + err.Error())
	}
	aaguid := [16]byte{}
	// A fixed non-zero AAGUID, so the stored row carries a model the view
	// can render.
	aaguid[0] = 0x0e
	aaguid[1] = 0xa2
	return &SoftAuthenticator{
		credential:       &SoftCredential{ID: randomBytes(32), Key: key, AAGUID: aaguid},
		backupEligible:   backupEligible,
		backupState:      backupState,
		userVerification: userVerification,
	}
}

// CredentialID exposes the credential's raw identifier.
func (a *SoftAuthenticator) CredentialID() []byte { return a.credential.ID }

func randomBytes(n int) []byte {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		panic("softauthn: random: " + err.Error())
	}
	return raw
}

// b64url is the base64url the WebAuthn JSON speaks, unpadded.
var b64url = base64.RawURLEncoding

// clientData builds the client data JSON a ceremony step answers with.
func clientData(typ, challenge, origin string) []byte {
	raw, err := json.Marshal(map[string]string{
		"type":      typ,
		"challenge": challenge,
		"origin":    origin,
	})
	if err != nil {
		panic("softauthn: client data: " + err.Error())
	}
	return raw
}

// rpIDHash is sha256 over the RP ID — the binding the verification checks.
func rpIDHash(rpID string) []byte {
	sum := sha256.Sum256([]byte(rpID))
	return sum[:]
}

// authData builds the authenticator data: the RP ID hash, the flags, the
// counter, and — for a registration — the attested credential data with its
// COSE key.
func (a *SoftAuthenticator) authData(rpID string, registration bool) []byte {
	flags := byte(flagUP)
	if registration {
		flags |= flagAT
	}
	if a.userVerification {
		flags |= flagUV
	}
	if a.backupEligible {
		flags |= flagBE
	}
	if a.backupState {
		flags |= flagBS
	}

	data := rpIDHash(rpID)
	data = append(data, flags)
	data = binary.BigEndian.AppendUint32(data, a.counter)
	if registration {
		data = append(data, a.credential.AAGUID[:]...)
		data = binary.BigEndian.AppendUint16(data, uint16(len(a.credential.ID)))
		data = append(data, a.credential.ID...)
		data = append(data, coseKey(a.credential.Key)...)
	}
	return data
}

// coseKey encodes the credential's public key in the COSE form the
// attestation object carries: EC2/P-256/ES256 with the raw coordinates.
// The coordinates come from the key's uncompressed encoding — the raw
// big.Int fields are deprecated, and the encoding is their supported read.
func coseKey(key *ecdsa.PrivateKey) []byte {
	uncompressed, err := key.PublicKey.Bytes()
	if err != nil {
		panic("softauthn: key bytes: " + err.Error())
	}
	cose := map[int]any{
		1:  2,  // kty: EC2
		3:  -7, // alg: ES256
		-1: 1,  // crv: P-256
		-2: uncompressed[1:33],
		-3: uncompressed[33:65],
	}
	raw, err := cbor.Marshal(cose)
	if err != nil {
		panic("softauthn: cose key: " + err.Error())
	}
	return raw
}

// optionsChallenge reads the challenge and RP ID an options document
// carries. A creation document names the RP in its rp member; an assertion
// document carries the bare rpId — the entity the browser already knows.
func optionsChallenge(t *testing.T, options string) (string, string) {
	t.Helper()
	var doc struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			RP        struct {
				ID string `json:"id"`
			} `json:"rp"`
			RPID string `json:"rpId"`
		} `json:"publicKey"`
	}
	require.NoError(t, json.Unmarshal([]byte(options), &doc))
	rpID := doc.PublicKey.RP.ID
	if rpID == "" {
		rpID = doc.PublicKey.RPID
	}
	return doc.PublicKey.Challenge, rpID
}

// Create answers the registration ceremony: the attestation object carries
// the "none" format — no attestation statement, the self-attestation every
// passkey effectively is.
func (a *SoftAuthenticator) Create(t *testing.T, options, origin string) string {
	t.Helper()
	challenge, rpID := optionsChallenge(t, options)

	authData := a.authData(rpID, true)
	attestation := map[string]any{
		"fmt":      "none",
		"attStmt":  map[string]any{},
		"authData": authData,
	}
	object, err := cbor.Marshal(attestation)
	require.NoError(t, err)

	response := map[string]any{
		"id":    b64url.EncodeToString(a.credential.ID),
		"rawId": b64url.EncodeToString(a.credential.ID),
		"type":  "public-key",
		"response": map[string]string{
			"clientDataJSON":    b64url.EncodeToString(clientData("webauthn.create", challenge, origin)),
			"attestationObject": b64url.EncodeToString(object),
		},
	}
	raw, err := json.Marshal(response)
	require.NoError(t, err)
	return string(raw)
}

// Get answers the authentication ceremony: the signature covers the
// authenticator data and the client data's hash, the way the verification
// re-derives it.
func (a *SoftAuthenticator) Get(t *testing.T, options, origin string, userHandle []byte) string {
	t.Helper()
	challenge, rpID := optionsChallenge(t, options)

	a.counter++
	authData := a.authData(rpID, false)
	clientDataJSON := clientData("webauthn.get", challenge, origin)

	clientHash := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte{}, authData...), clientHash[:]...)
	// ECDSA signs the SHA-256 digest of the signed bytes — authData
	// followed by the client data's hash — the way the verifier re-derives
	// it.
	digest := sha256.Sum256(signed)
	signature, err := ecdsa.SignASN1(rand.Reader, a.credential.Key, digest[:])
	require.NoError(t, err)

	response := map[string]any{
		"id":    b64url.EncodeToString(a.credential.ID),
		"rawId": b64url.EncodeToString(a.credential.ID),
		"type":  "public-key",
		"response": map[string]string{
			"authenticatorData": b64url.EncodeToString(authData),
			"clientDataJSON":    b64url.EncodeToString(clientDataJSON),
			"signature":         b64url.EncodeToString(signature),
			"userHandle":        b64url.EncodeToString(userHandle),
		},
	}
	raw, err := json.Marshal(response)
	require.NoError(t, err)
	return string(raw)
}
