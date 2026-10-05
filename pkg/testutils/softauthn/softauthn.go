// Package softauthn is a real WebAuthn authenticator in Go: one ECDSA key
// pair per credential, genuine attestation (none format) and assertion
// construction, and the flags and counter a test asks for. It exists so a
// test or an E2E driver can answer the ceremonies the server opens with
// everything the protocol requires and nothing a browser adds.
//
// It is a test instrument, not a dependency of any feature.
package softauthn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"

	"github.com/fxamacker/cbor/v2"
)

// Authenticator flags. UP is always set — an assertion without user presence
// is no assertion; UV answers the verification requirement; BE and BS are
// the backup flags the synced-passkey policy judges; AT marks the attested
// credential data a registration's authenticator data carries.
const (
	FlagUP = 0x01
	FlagUV = 0x04
	FlagBE = 0x08
	FlagBS = 0x10
	FlagAT = 0x40
)

// Credential is one credential the authenticator holds.
type Credential struct {
	ID     []byte
	Key    *ecdsa.PrivateKey
	AAGUID [16]byte
}

// Authenticator is the test's device.
type Authenticator struct {
	credential *Credential
	// counter is the signature counter the next assertion reports.
	counter uint32
	// backupEligible and backupState are the flags assertions report.
	backupEligible bool
	backupState    bool
	// userVerification is whether assertions carry the UV flag.
	userVerification bool
}

// New builds a device holding one fresh credential.
func New(backupEligible, backupState, userVerification bool) *Authenticator {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic("softauthn: key: " + err.Error())
	}
	aaguid := [16]byte{}
	// A fixed non-zero AAGUID, so a stored row carries a model a view can
	// render.
	aaguid[0] = 0x0e
	aaguid[1] = 0xa2
	return &Authenticator{
		credential:       &Credential{ID: randomBytes(32), Key: key, AAGUID: aaguid},
		backupEligible:   backupEligible,
		backupState:      backupState,
		userVerification: userVerification,
	}
}

// CredentialID exposes the credential's raw identifier.
func (a *Authenticator) CredentialID() []byte { return a.credential.ID }

// RewindCounter sets the next assertion's counter to zero — the move a
// clone test makes: the same key claiming less use than the row remembers.
func (a *Authenticator) RewindCounter() { a.counter = 0 }

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
// COSE key. The coordinates come from the key's uncompressed encoding; the
// raw big.Int fields are deprecated, and the encoding is their supported
// read.
func (a *Authenticator) authData(rpID string, registration bool) []byte {
	flags := byte(FlagUP)
	if registration {
		flags |= FlagAT
	}
	if a.userVerification {
		flags |= FlagUV
	}
	if a.backupEligible {
		flags |= FlagBE
	}
	if a.backupState {
		flags |= FlagBS
	}

	data := rpIDHash(rpID)
	data = append(data, flags)
	data = binary.BigEndian.AppendUint32(data, a.counter)
	if registration {
		data = append(data, a.credential.AAGUID[:]...)
		idLength := len(a.credential.ID)
		if idLength > 0xffff {
			panic("softauthn: credential id exceeds the protocol's length field")
		}
		data = binary.BigEndian.AppendUint16(data, uint16(idLength))
		data = append(data, a.credential.ID...)
		data = append(data, coseKey(a.credential.Key)...)
	}
	return data
}

// coseKey encodes the credential's public key in the COSE form the
// attestation object carries: EC2/P-256/ES256 with the raw coordinates.
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

// Options reads the challenge and RP ID an options document carries. A
// creation document names the RP in its rp member; an assertion document
// carries the bare rpId — the entity the browser already knows.
func Options(document string) (challenge, rpID string, err error) {
	var doc struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			RP        struct {
				ID string `json:"id"`
			} `json:"rp"`
			RPID string `json:"rpId"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal([]byte(document), &doc); err != nil {
		return "", "", err
	}
	rpID = doc.PublicKey.RP.ID
	if rpID == "" {
		rpID = doc.PublicKey.RPID
	}
	return doc.PublicKey.Challenge, rpID, nil
}

// Create answers the registration ceremony: the attestation object carries
// the "none" format — no attestation statement, the self-attestation every
// passkey effectively is.
func (a *Authenticator) Create(options, origin string) (string, error) {
	challenge, rpID, err := Options(options)
	if err != nil {
		return "", err
	}

	authData := a.authData(rpID, true)
	attestation := map[string]any{
		"fmt":      "none",
		"attStmt":  map[string]any{},
		"authData": authData,
	}
	object, err := cbor.Marshal(attestation)
	if err != nil {
		return "", err
	}

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
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Get answers the authentication ceremony: the signature covers the SHA-256
// digest of the authenticator data and the client data's hash, the way the
// verification re-derives it. The ES256 signature is ASN.1 DER — the
// encoding the verifier requires canonical.
func (a *Authenticator) Get(options, origin string, userHandle []byte) (string, error) {
	challenge, rpID, err := Options(options)
	if err != nil {
		return "", err
	}

	a.counter++
	authData := a.authData(rpID, false)
	clientDataJSON := clientData("webauthn.get", challenge, origin)

	clientHash := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte{}, authData...), clientHash[:]...)
	digest := sha256.Sum256(signed)
	signature, err := ecdsa.SignASN1(rand.Reader, a.credential.Key, digest[:])
	if err != nil {
		return "", err
	}

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
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
