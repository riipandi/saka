// e2e-passkey drives the whole passkey authentication ladder against a live
// server — the wire-level E2E the passkey plan's Phase D runs. The soft
// authenticator answers the ceremonies the way a browser would; every other
// step speaks ConnectRPC to the running binary.
//
// The scenarios, in one session over a freshly reset and seeded database:
//
//	S1  signup (under an admin-issued token) → password sign-in → enroll a
//	    passkey → sign in passwordless.
//	S2  the MFA account: passkey sign-in is a whole session in one step;
//	    a password sign-in mints the bridge, completed once by the passkey,
//	    once by a TOTP code, once by a recovery code; three wrong codes end
//	    a bridge; a one-time code mints the bridge, not a session.
//	S3  the passwordless account: a one-time code opens the session
//	    directly; the passkey it then enrolls cannot be deleted — the
//	    stranding refusal holds at the administrator's door too.
//	S4  the step-up: a password proof and a passkey proof, spent on
//	    guarded calls independently, each replay refused.
//	S5  the settings live: the synced-passkey toggle and the credential
//	    limit are judged at enrollment, without a restart.
//	S6  the lifecycle: rename, admin list/rename/delete, delete-last
//	    allowed under a password.
//	S7  the negative set: a replayed assertion, an expired ceremony, an
//	    unknown credential, a banned account, a cloned credential.
//
// Usage: go run ./tools/e2e-passkey --base-url http://localhost:3080
//
// The admin credentials come from the environment: E2E_IDENTITY and
// E2E_PASSWORD (the seeded administrator). Every artifact the run creates —
// accounts, credentials, sessions, ceremony rows, audit rows — is removed
// by the caller afterwards; the run writes nothing to disk.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/testutils/softauthn"
)

const rpcPath = "/rpc/"

func main() {
	baseURL := flag.String("base-url", "http://localhost:3080", "the server's base URL")
	insecure := flag.Bool("insecure", false, "skip the TLS verification - the self-signed development proxy")
	flag.Parse()

	adminIdentity := os.Getenv("E2E_IDENTITY")
	adminPassword := os.Getenv("E2E_PASSWORD")
	if adminIdentity == "" || adminPassword == "" {
		fail("E2E_IDENTITY and E2E_PASSWORD must be set")
	}

	client := &rpcClient{base: strings.TrimSuffix(*baseURL, "/"), http: &http.Client{}}
	if *insecure {
		client.http.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // the self-signed development proxy
	}

	// ---- S0. The administrator opens the session every admin door runs on.

	admin := client.mustRPC("tango.authn.v1.AuthService/SignIn", map[string]any{
		"identity": adminIdentity,
		"password": adminPassword,
	}, "")
	adminToken := str(admin, "access_token")
	if adminToken == "" {
		fail("the admin sign-in answered no access token: %v", admin)
	}
	pass("admin signed in")

	// ---- S1. Signup under a token, password sign-in, passkey sign-in.

	sophie := signup(client, adminToken, "sophie", "Sophie Neveu")
	sophieToken := passwordSignIn(client, "sophie", passwordOf("sophie"))

	device := softauthn.New(false, false, true)
	enrolled := enroll(client, device, sophieToken, "E2E key")
	pass("passkey enrolled: %s", enrolled)

	passwordlessSignIn(client, device, handleOf(sophie))
	pass("sophie signed in passwordless")

	// ---- S2. The MFA account: the bridge forks, and the passkey is one
	// of the three keys that complete it.

	vittoria := signup(client, adminToken, "vittoria", "Vittoria Vetra")
	vittoriaToken := passwordSignIn(client, "vittoria", passwordOf("vittoria"))

	vittoriaDevice := softauthn.New(false, false, true)
	enroll(client, vittoriaDevice, vittoriaToken, "Vittoria key")
	totpID, totpSecret, recoveryCodes := enrollTotp(client, vittoriaToken)
	// A TOTP code is single-use per its thirty-second step — the replay is
	// the compare-and-set the row holds — so every completion in this run
	// takes a step the row has never seen. The step walks forward only when
	// the wall clock has not already moved past it: a code too far from the
	// verifier's window is as invalid as a replayed one.
	totpStep := uint64(time.Now().Unix()) / 30
	nextCode := func() string {
		nowStep := uint64(time.Now().Unix()) / 30
		// A consumed step must never repeat, and the verifier's window is
		// one step each way: when the run has spent the step the clock is
		// still in, the only honest code is the next boundary's — wait for
		// the wall clock to carry it into the window.
		if totpStep >= nowStep+1 {
			time.Sleep(time.Until(time.Unix(int64((totpStep+1)*30), 0)) + 2*time.Second)
			nowStep = uint64(time.Now().Unix()) / 30
		}
		if nowStep > totpStep {
			totpStep = nowStep
		} else {
			totpStep++
		}
		return totpCode(totpSecret, time.Unix(int64(totpStep)*30, 0))
	}
	pass("vittoria enrolled a passkey and TOTP (%s), %d recovery codes held", totpID, len(recoveryCodes))

	// The passkey assertion with user verification is the whole proof: the
	// session opens in one step, no bridge.
	options, sessionID := ceremony(client, "tango.authn.v1.WebAuthnService/BeginLogin", "")
	assertion, err := vittoriaDevice.Get(options, *baseURL, handleOf(vittoria))
	if err != nil {
		fail("soft authenticator get: %v", err)
	}
	oneStep := client.mustRPC("tango.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": assertion,
	}, "")
	if str(oneStep, "access_token") == "" {
		fail("the passkey sign-in on the MFA account answered no tokens: %v", oneStep)
	}
	pass("the passkey sign-in opened a whole session on the MFA account")

	// The password mints the bridge; the passkey completes it.
	bridge := passwordBridge(client, "vittoria", passwordOf("vittoria"))
	completeSignInPasskey(client, bridge, vittoriaDevice, handleOf(vittoria), *baseURL)
	pass("the passkey completed the bridge")

	// The same bridge, completed by the TOTP code.
	bridge = passwordBridge(client, "vittoria", passwordOf("vittoria"))
	completeSignInCode(client, bridge, nextCode())
	pass("the TOTP code completed the bridge")

	// And once by a recovery code — one of the ten single-use codes.
	bridge = passwordBridge(client, "vittoria", passwordOf("vittoria"))
	completeSignInCode(client, bridge, recoveryCodes[0])
	pass("a recovery code completed the bridge")

	// Three wrong codes end the bridge; a fresh password sign-in starts a
	// fresh one.
	bridge = passwordBridge(client, "vittoria", passwordOf("vittoria"))
	for range 3 {
		if code := client.tryRPC("tango.authn.v1.MultifactorService/CompleteSignIn", map[string]any{
			"pending_token": bridge,
			"code":          "000000",
		}, ""); code != http.StatusUnauthorized {
			fail("a wrong code answered %d; the refusal must be unauthenticated", code)
		}
	}
	// The dead bridge is probed with a code that never verifies — a live
	// code would burn its step at the verify, before the bridge's death is
	// judged, and the run's next honest completion would pay for it.
	if code := client.tryRPC("tango.authn.v1.MultifactorService/CompleteSignIn", map[string]any{
		"pending_token": bridge,
		"code":          "000000",
	}, ""); code != http.StatusUnauthorized {
		fail("a code on a dead bridge answered %d; the refusal must be unauthenticated", code)
	}
	pass("three wrong codes ended the bridge")

	// The one-time code on the MFA account mints the bridge, not a session.
	oneTimeCode := issueOneTimeCode(client, adminToken, vittoria)
	exchange := client.mustRPC("tango.authn.v1.OneTimeAccessService/ExchangeToken", map[string]any{
		"token": oneTimeCode,
	}, "")
	if !anyTrue(exchange["mfa_required"]) || str(exchange, "mfa_pending_token") == "" {
		fail("the exchange on the MFA account answered no bridge: %v", exchange)
	}
	completeSignInCode(client, str(exchange, "mfa_pending_token"), nextCode())
	pass("the one-time code minted the bridge on the MFA account")

	// ---- S3. The passwordless account: the code opens the session, and
	// the passkey it enrolls cannot be taken away.

	silas := adminCreateUser(client, adminToken, "silas", "Silas")
	oneTimeCode = issueOneTimeCode(client, adminToken, silas)
	exchange = client.mustRPC("tango.authn.v1.OneTimeAccessService/ExchangeToken", map[string]any{
		"token": oneTimeCode,
	}, "")
	silasToken := str(exchange, "access_token")
	if silasToken == "" {
		fail("the exchange on the passwordless account answered no tokens: %v", exchange)
	}
	pass("the one-time code opened the passwordless account's session directly")

	silasDevice := softauthn.New(false, false, true)
	silasCredential := enroll(client, silasDevice, silasToken, "Silas key")

	if code := client.tryRPC("tango.authn.v1.WebAuthnService/AdminDeleteCredential", map[string]any{
		"user_id":       silas,
		"credential_id": silasCredential,
	}, adminToken); code != http.StatusBadRequest {
		fail("the stranding delete answered %d; the refusal must be failed-precondition", code)
	}
	pass("the stranding refusal held at the administrator's door")

	// ---- S4. The step-up: two proofs, spent independently, each dead
	// after its one spend. The guarded door is RevokeSession — vittoria
	// keeps the sessions the bridges opened; the proofs end two of them.
	//
	// vittoriaToken predates the MFA enrollment and still carries: the
	// token is the authority, the proof is the fresh credential.

	sessionA := currentSession(client, vittoriaToken)
	sessionB := passwordlessSession(client, vittoriaDevice, handleOf(vittoria))

	proofPassword := mintProof(client, map[string]any{
		"password": passwordOf("vittoria"),
	}, vittoriaToken)
	proofPasskey := mintPasskeyProof(client, vittoriaDevice, handleOf(vittoria), *baseURL, vittoriaToken)
	pass("the step-up proofs minted, one by password and one by passkey")

	spendProof(client, vittoriaToken, proofPassword, sessionA)
	spendProof(client, vittoriaToken, proofPasskey, sessionB)
	pass("the two proofs spent independently")

	// ---- S5. The settings live: the toggle and the limit are judged at
	// enrollment, no restart in between.

	settingsUpdate(client, adminToken, "webauthn.allow_synced_passkeys", "false")
	syncedOptions, syncedSession := ceremony(client, "tango.authn.v1.WebAuthnService/BeginRegistration", vittoriaToken)
	synced := softauthn.New(true, true, true)
	if code := client.tryRPC("tango.authn.v1.WebAuthnService/VerifyRegistration", map[string]any{
		"session_id": syncedSession,
		"credential": mustCreate(synced, syncedOptions, *baseURL),
		"name":       "Synced key",
	}, vittoriaToken); code != http.StatusBadRequest {
		fail("the synced enrollment under the off toggle answered %d", code)
	}
	settingsUpdate(client, adminToken, "webauthn.allow_synced_passkeys", "true")
	pass("the synced-passkey toggle is judged at enrollment")

	settingsUpdate(client, adminToken, "passkey.max_credentials", "1")
	options, sessionID = ceremony(client, "tango.authn.v1.WebAuthnService/BeginRegistration", vittoriaToken)
	if code := client.tryRPC("tango.authn.v1.WebAuthnService/VerifyRegistration", map[string]any{
		"session_id": sessionID,
		"credential": mustCreate(softauthn.New(false, false, true), options, *baseURL),
		"name":       "Beyond the limit",
	}, vittoriaToken); code != http.StatusBadRequest {
		fail("the enrollment past the lowered limit answered %d", code)
	}
	settingsUpdate(client, adminToken, "passkey.max_credentials", "10")
	pass("the credential limit is judged at enrollment")

	// ---- S6. The lifecycle: rename by the holder, the admin doors over
	// another account, the delete-last allowed under a password.

	renamed := client.mustRPC("tango.authn.v1.WebAuthnService/UpdateCredential", map[string]any{
		"credential_id": enrolled,
		"name":          "Sophie's laptop",
	}, sophieToken)
	if str(renamed, "credential.name") != "Sophie's laptop" {
		fail("the rename answered the wrong name: %v", renamed)
	}
	pass("the holder renamed their passkey")

	adminRoll := client.mustRPC("tango.authn.v1.WebAuthnService/AdminListCredentials", map[string]any{
		"user_id": sophie,
	}, adminToken)
	adminList, _ := adminRoll["credentials"].([]any)
	if len(adminList) != 1 {
		fail("the admin roll carries %d credentials; sophie enrolled one", len(adminList))
	}
	client.mustRPC("tango.authn.v1.WebAuthnService/AdminRenameCredential", map[string]any{
		"user_id":       sophie,
		"credential_id": enrolled,
		"name":          "Renamed by the operator",
	}, adminToken)
	client.mustRPC("tango.authn.v1.WebAuthnService/AdminDeleteCredential", map[string]any{
		"user_id":       sophie,
		"credential_id": enrolled,
	}, adminToken)
	pass("the admin doors listed, renamed, and removed sophie's passkey")

	// Delete-last is allowed: the password is the way back in. Sophie
	// re-enrolls to have one to lose, then loses it.
	second := enroll(client, device, sophieToken, "Second key")
	client.mustRPC("tango.authn.v1.WebAuthnService/DeleteCredential", map[string]any{
		"credential_id": second,
	}, sophieToken, "X-Tango-Reauthentication", reauthPassword(client, sophieToken, passwordOf("sophie")))
	pass("the delete-last allowed under a password")

	// ---- S7. The negative set.

	// A replayed assertion: the same body twice, the second refused.
	options, sessionID = ceremony(client, "tango.authn.v1.WebAuthnService/BeginLogin", "")
	assertion = mustGet(vittoriaDevice, options, *baseURL, handleOf(vittoria))
	firstCode := client.tryRPC("tango.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": assertion,
	}, "")
	if firstCode != http.StatusOK {
		fail("the fresh assertion answered %d", firstCode)
	}
	// The same assertion over the same, already-consumed ceremony handle.
	if code := client.tryRPC("tango.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": assertion,
	}, ""); code != http.StatusUnauthorized {
		fail("the replayed assertion answered %d", code)
	}
	pass("the replayed assertion refused")

	// An expired ceremony: a minute of silence, then the verify refuses.
	options, sessionID = ceremony(client, "tango.authn.v1.WebAuthnService/BeginLogin", "")
	time.Sleep(61 * time.Second)
	if code := client.tryRPC("tango.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": mustGet(vittoriaDevice, options, *baseURL, handleOf(vittoria)),
	}, ""); code != http.StatusUnauthorized {
		fail("the expired ceremony answered %d", code)
	}
	pass("the expired ceremony refused")

	// An unknown credential: an authenticator sophie never enrolled.
	stranger := softauthn.New(false, false, true)
	options, sessionID = ceremony(client, "tango.authn.v1.WebAuthnService/BeginLogin", "")
	if code := client.tryRPC("tango.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": mustGet(stranger, options, *baseURL, handleOf(sophie)),
	}, ""); code != http.StatusUnauthorized {
		fail("the unknown credential answered %d", code)
	}
	pass("the unknown credential refused")

	// A banned account: sophie's passkey is real, the account is closed.
	client.mustRPC("tango.identity.v1.UserService/BanUser", map[string]any{
		"id":     sophie,
		"reason": "the E2E ladder is holding the door",
	}, adminToken)
	options, sessionID = ceremony(client, "tango.authn.v1.WebAuthnService/BeginLogin", "")
	if code := client.tryRPC("tango.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": mustGet(device, options, *baseURL, handleOf(sophie)),
	}, ""); code != http.StatusUnauthorized {
		fail("the banned account answered %d", code)
	}
	client.mustRPC("tango.identity.v1.UserService/UnbanUser", map[string]any{
		"id": sophie,
	}, adminToken)
	pass("the banned account refused, then unbanned")

	// A cloned credential: the counter rewinds, the refusal is silent.
	vittoriaDevice.RewindCounter()
	options, sessionID = ceremony(client, "tango.authn.v1.WebAuthnService/BeginLogin", "")
	if code := client.tryRPC("tango.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": mustGet(vittoriaDevice, options, *baseURL, handleOf(vittoria)),
	}, ""); code != http.StatusBadRequest {
		fail("the cloned credential answered %d", code)
	}
	pass("the cloned credential refused")

	fmt.Println("status: the passkey ladder ran clean")
}

// handleOf reads an account's wire form into the 16 bytes the assertion's
// userHandle carries.
func handleOf(wire string) []byte {
	id, err := user.UUIDFromWire(wire)
	if err != nil {
		fail("the account id does not decode: %v", err)
	}
	return id[:]
}

func mustCreate(device *softauthn.Authenticator, options, origin string) string {
	response, err := device.Create(options, origin)
	if err != nil {
		fail("soft authenticator create: %v", err)
	}
	return response
}

func mustGet(device *softauthn.Authenticator, options, origin string, handle []byte) string {
	assertion, err := device.Get(options, origin, handle)
	if err != nil {
		fail("soft authenticator get: %v", err)
	}
	return assertion
}

// passwordlessSession opens a session by the passkey alone and answers its
// access token — the one-step sign-in the MFA-enabled account also answers.
func passwordlessSession(client *rpcClient, device *softauthn.Authenticator, handle []byte) string {
	options, sessionID := ceremony(client, "tango.authn.v1.WebAuthnService/BeginLogin", "")
	answer := client.mustRPC("tango.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": mustGet(device, options, client.base, handle),
	}, "")
	token := str(answer, "access_token")
	if token == "" {
		fail("the passkey sign-in answered no access token: %v", answer)
	}
	return token
}

// mintProof asks Reauthenticate by whatever the body carries and answers
// the raw token, in clear text, exactly once.
func mintProof(client *rpcClient, proof map[string]any, accessToken string) string {
	answer := client.mustRPC("tango.authn.v1.WebAuthnService/Reauthenticate", proof, accessToken)
	token := str(answer, "token")
	if token == "" {
		fail("the reauthentication answered no token: %v", answer)
	}
	return token
}

// mintPasskeyProof walks the sign-in ceremony for the assertion the
// step-up proof rides.
func mintPasskeyProof(client *rpcClient, device *softauthn.Authenticator, handle []byte, origin, accessToken string) string {
	options, sessionID := ceremony(client, "tango.authn.v1.WebAuthnService/BeginLogin", "")
	return mintProof(client, map[string]any{
		"passkey": map[string]string{
			"session_id": sessionID,
			"credential": mustGet(device, options, origin, handle),
		},
	}, accessToken)
}

// spendProof spends the header proof on the guarded revocation and holds
// the replay to the unauthenticated refusal.
func spendProof(client *rpcClient, accessToken, proof, sessionID string) {
	client.mustRPC("tango.authn.v1.SessionService/RevokeSession", map[string]any{
		"id": sessionID,
	}, accessToken, "X-Tango-Reauthentication", proof)

	if code := client.tryRPC("tango.authn.v1.SessionService/RevokeSession", map[string]any{
		"id": sessionID,
	}, accessToken, "X-Tango-Reauthentication", proof); code != http.StatusUnauthorized {
		fail("the spent proof answered %d; the refusal must be unauthenticated", code)
	}
}

// reauthPassword mints the step-up proof the guarded delete needs.
func reauthPassword(client *rpcClient, accessToken, password string) string {
	return mintProof(client, map[string]any{"password": password}, accessToken)
}

// settingsUpdate writes one setting through the admin surface.
func settingsUpdate(client *rpcClient, adminToken, key, value string) {
	client.mustRPC("tango.settings.v1.SettingsService/Update", map[string]any{
		"key":   key,
		"value": value,
	}, adminToken)
}

// ---- the helpers the scenarios walk ----

func signup(client *rpcClient, adminToken, username, displayName string) string {
	first, last := displayName, ""
	if idx := strings.IndexByte(displayName, ' '); idx > 0 {
		first, last = displayName[:idx], displayName[idx+1:]
	}
	token := client.mustRPC("tango.identity.v1.SignupService/CreateSignupToken", map[string]any{
		"ttl_seconds": 86400,
	}, adminToken)
	created := client.mustRPC("tango.identity.v1.SignupService/Signup", map[string]any{
		"username":  username,
		"email":     username + "@example.com",
		"password":  passwordOf(username),
		"token":     str(token, "raw_token"),
		"firstName": first,
		"lastName":  last,
	}, "")
	wire := str(created, "user.id")
	if wire == "" {
		fail("the signup answered no account id: %v", created)
	}
	pass("%s signed up under an issued token", username)
	return wire
}

func adminCreateUser(client *rpcClient, adminToken, username, displayName string) string {
	created := client.mustRPC("tango.identity.v1.UserService/CreateUser", map[string]any{
		"username":   username,
		"email":      username + "@example.com",
		"first_name": displayName,
		"last_name":  "of E2E",
	}, adminToken)
	wire := str(created, "user.id")
	if wire == "" {
		fail("the create answered no account id: %v", created)
	}
	pass("%s created without a password", username)
	return wire
}

func passwordSignIn(client *rpcClient, identity, password string) string {
	answer := client.mustRPC("tango.authn.v1.AuthService/SignIn", map[string]any{
		"identity": identity,
		"password": password,
	}, "")
	token := str(answer, "access_token")
	if token == "" {
		fail("the password sign-in answered no access token: %v", answer)
	}
	return token
}

// passwordBridge signs in with the password on an MFA-enabled account and
// answers the pending token the bridge carries.
func passwordBridge(client *rpcClient, identity, password string) string {
	answer := client.mustRPC("tango.authn.v1.AuthService/SignIn", map[string]any{
		"identity": identity,
		"password": password,
	}, "")
	if !anyTrue(answer["mfa_required"]) {
		fail("the password sign-in answered no bridge: %v", answer)
	}
	return str(answer, "mfa_pending_token")
}

func completeSignInCode(client *rpcClient, pending, code string) {
	client.mustRPC("tango.authn.v1.MultifactorService/CompleteSignIn", map[string]any{
		"pending_token": pending,
		"code":          code,
	}, "")
}

func completeSignInPasskey(client *rpcClient, pending string, device *softauthn.Authenticator, userHandle []byte, origin string) {
	options, sessionID := ceremony(client, "tango.authn.v1.WebAuthnService/BeginLogin", "")
	assertion, err := device.Get(options, origin, userHandle)
	if err != nil {
		fail("soft authenticator get: %v", err)
	}
	client.mustRPC("tango.authn.v1.MultifactorService/CompleteSignIn", map[string]any{
		"pending_token": pending,
		"passkey": map[string]string{
			"session_id": sessionID,
			"credential": assertion,
		},
	}, "")
}

func passwordlessSignIn(client *rpcClient, device *softauthn.Authenticator, userHandle []byte) {
	options, sessionID := ceremony(client, "tango.authn.v1.WebAuthnService/BeginLogin", "")
	assertion, err := device.Get(options, client.base, userHandle)
	if err != nil {
		fail("soft authenticator get: %v", err)
	}
	answer := client.mustRPC("tango.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": assertion,
	}, "")
	if str(answer, "access_token") == "" {
		fail("the passkey sign-in answered no access token: %v", answer)
	}
}

// enroll walks the registration ceremony and answers the enrolled
// credential's wire identifier.
func enroll(client *rpcClient, device *softauthn.Authenticator, accessToken, name string) string {
	options, sessionID := ceremony(client, "tango.authn.v1.WebAuthnService/BeginRegistration", accessToken)
	response, err := device.Create(options, client.base)
	if err != nil {
		fail("soft authenticator create: %v", err)
	}
	verified := client.mustRPC("tango.authn.v1.WebAuthnService/VerifyRegistration", map[string]any{
		"session_id": sessionID,
		"credential": response,
		"name":       name,
	}, accessToken)
	enrolled := str(verified, "credential.id")
	if enrolled == "" {
		fail("the verify answered no credential id: %v", verified)
	}
	return enrolled
}

// enrollTotp walks the TOTP enrollment and answers the confirmed
// enrollment's id, its secret, and the recovery codes shown once.
func enrollTotp(client *rpcClient, accessToken string) (totpID, secret string, recoveryCodes []string) {
	begun := client.mustRPC("tango.authn.v1.MultifactorService/BeginTotpEnrollment", map[string]any{
		"name": "E2E device",
	}, accessToken)
	totpID, secret = str(begun, "totp_id"), str(begun, "secret")
	if totpID == "" || secret == "" {
		fail("the TOTP enrollment began without a secret: %v", begun)
	}
	confirmed := client.mustRPC("tango.authn.v1.MultifactorService/ConfirmTotpEnrollment", map[string]any{
		"totp_id": totpID,
		"code":    totpCode(secret, time.Now()),
	}, accessToken)
	for _, entry := range confirmed["recovery_codes"].([]any) {
		recoveryCodes = append(recoveryCodes, entry.(string))
	}
	if len(recoveryCodes) == 0 {
		fail("the TOTP confirm answered no recovery codes: %v", confirmed)
	}
	return totpID, secret, recoveryCodes
}

func issueOneTimeCode(client *rpcClient, adminToken, userWire string) string {
	issued := client.mustRPC("tango.authn.v1.OneTimeAccessService/CreateToken", map[string]any{
		"id": userWire,
	}, adminToken)
	code := str(issued, "token")
	if code == "" {
		fail("the one-time issue answered no code: %v", issued)
	}
	return code
}

func currentSession(client *rpcClient, accessToken string) string {
	sessions := client.mustRPC("tango.authn.v1.SessionService/ListSessions", map[string]any{}, accessToken)
	list, _ := sessions["sessions"].([]any)
	if len(list) == 0 {
		fail("the session list answered nothing: %v", sessions)
	}
	first, _ := list[0].(map[string]any)
	id, _ := first["id"].(string)
	if id == "" {
		fail("the session list carries no readable id: %v", sessions)
	}
	return id
}

func passwordOf(username string) string { return "@" + username + "-passkey-e2e" }

// totpCode renders the six-digit value the authenticator shows at now —
// the RFC 6238 SHA-1 default, thirty-second steps.
func totpCode(base32Secret string, at time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(base32Secret, "=")))
	if err != nil {
		fail("the TOTP secret does not decode: %v", err)
	}
	step := uint64(at.Unix()) / 30
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], step)
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", value)
}

func mustUserHandle(signIn map[string]any) []byte {
	userField, _ := signIn["user"].(map[string]any)
	if userField == nil {
		fail("the sign-in answer carries no account view")
	}
	id, err := user.UUIDFromWire(str(userField, "id"))
	if err != nil {
		fail("the sign-in answer carries no decodable account id: %v", err)
	}
	return id[:]
}

func ceremony(client *rpcClient, procedure, accessToken string) (options, sessionID string) {
	answer := client.mustRPC(procedure, map[string]any{}, accessToken)
	options = str(answer, "options")
	sessionID = str(answer, "session_id")
	if options == "" || sessionID == "" {
		fail("%s answered an incomplete ceremony: %v", procedure, answer)
	}
	return options, sessionID
}

func str(document map[string]any, field string) string {
	if !strings.Contains(field, ".") {
		value, _ := document[field].(string)
		return value
	}
	head, tail, _ := strings.Cut(field, ".")
	next, _ := document[head].(map[string]any)
	if next == nil {
		return ""
	}
	return str(next, tail)
}

func anyTrue(value any) bool { return value == true }

func pass(format string, args ...any) { fmt.Println("  ok  " + fmt.Sprintf(format, args...)) }

func fail(format string, args ...any) {
	fmt.Println("FAIL  " + fmt.Sprintf(format, args...))
	os.Exit(1)
}

type rpcClient struct {
	base string
	http *http.Client
}

func (c *rpcClient) call(procedure, body, accessToken string, headers ...string) (int, map[string]any) {
	request, err := http.NewRequest(http.MethodPost, c.base+rpcPath+procedure, bytes.NewBufferString(body))
	if err != nil {
		fail("the request does not build: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}

	response, err := c.http.Do(request)
	if err != nil {
		fail("%s is unreachable: %v", procedure, err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)

	var document map[string]any
	_ = json.Unmarshal(raw, &document)
	return response.StatusCode, document
}

func (c *rpcClient) mustRPC(procedure string, body map[string]any, accessToken string, headers ...string) map[string]any {
	raw, _ := json.Marshal(body)
	code, document := c.call(procedure, string(raw), accessToken, headers...)
	if code >= 300 {
		fail("%s answered %d: %s", procedure, code, mustJSON(document))
	}
	return document
}

func (c *rpcClient) tryRPC(procedure string, body map[string]any, accessToken string, headers ...string) int {
	raw, _ := json.Marshal(body)
	code, _ := c.call(procedure, string(raw), accessToken, headers...)
	return code
}

func mustJSON(document map[string]any) string {
	raw, err := json.Marshal(document)
	if err != nil {
		return fmt.Sprintf("%v", document)
	}
	return string(raw)
}
