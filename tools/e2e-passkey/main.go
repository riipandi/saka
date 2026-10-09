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
// Usage: go run ./tools/e2e-passkey --base-url http://localhost:3000
//
// The admin credentials come from the environment: E2E_IDENTITY and
// E2E_PASSWORD (the seeded administrator). Every artifact the run creates —
// accounts, credentials, sessions, ceremony rows, audit rows — is removed
// by the caller afterwards; the run writes nothing to disk.
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/riipandi/saka/pkg/testutils/softauthn"
)

func main() {
	baseURL := flagBaseURL()
	insecure := flagInsecure()

	adminIdentity := os.Getenv("E2E_IDENTITY")
	adminPassword := os.Getenv("E2E_PASSWORD")
	if adminIdentity == "" || adminPassword == "" {
		fail("E2E_IDENTITY and E2E_PASSWORD must be set")
	}

	client := newClient(baseURL, insecure)

	// ---- S0. The administrator opens the session every admin door runs on.

	admin := client.mustRPC("saka.authn.v1.AuthService/SignIn", map[string]any{
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
	pass("vittoria enrolled a passkey and TOTP (%s), %d recovery codes held", totpID, len(recoveryCodes))
	codes := newTotpClock(totpSecret)

	// The passkey assertion with user verification is the whole proof: the
	// session opens in one step, no bridge.
	oneStep := passwordlessSession(client, vittoriaDevice, handleOf(vittoria))
	if oneStep == "" {
		fail("the passkey sign-in on the MFA account answered no session")
	}
	pass("the passkey sign-in opened a whole session on the MFA account")

	// The password mints the bridge; the passkey completes it.
	bridge := passwordBridge(client, "vittoria", passwordOf("vittoria"))
	completeSignInPasskey(client, bridge, vittoriaDevice, handleOf(vittoria), baseURL)
	pass("the passkey completed the bridge")

	// The same bridge, completed by the TOTP code.
	bridge = passwordBridge(client, "vittoria", passwordOf("vittoria"))
	completeSignInCode(client, bridge, codes.next())
	pass("the TOTP code completed the bridge")

	// And once by a recovery code — one of the ten single-use codes.
	bridge = passwordBridge(client, "vittoria", passwordOf("vittoria"))
	completeSignInCode(client, bridge, recoveryCodes[0])
	pass("a recovery code completed the bridge")

	// Three wrong codes end the bridge; the dead bridge is probed with a
	// code that never verifies — a live code would burn its step at the
	// verify, before the bridge's death is judged, and the run's next
	// honest completion would pay for it.
	bridge = passwordBridge(client, "vittoria", passwordOf("vittoria"))
	for range 3 {
		if code := client.tryRPC("saka.authn.v1.MultifactorService/CompleteSignIn", map[string]any{
			"pending_token": bridge,
			"code":          "000000",
		}, ""); code != http.StatusUnauthorized {
			fail("a wrong code answered %d; the refusal must be unauthenticated", code)
		}
	}
	if code := client.tryRPC("saka.authn.v1.MultifactorService/CompleteSignIn", map[string]any{
		"pending_token": bridge,
		"code":          "000000",
	}, ""); code != http.StatusUnauthorized {
		fail("a code on a dead bridge answered %d; the refusal must be unauthenticated", code)
	}
	pass("three wrong codes ended the bridge")

	// The one-time code on the MFA account mints the bridge, not a session.
	oneTimeCode := issueOneTimeCode(client, adminToken, vittoria)
	exchange := client.mustRPC("saka.authn.v1.OneTimeAccessService/ExchangeToken", map[string]any{
		"token": oneTimeCode,
	}, "")
	if !anyTrue(exchange["mfa_required"]) || str(exchange, "mfa_pending_token") == "" {
		fail("the exchange on the MFA account answered no bridge: %v", exchange)
	}
	completeSignInCode(client, str(exchange, "mfa_pending_token"), codes.next())
	pass("the one-time code minted the bridge on the MFA account")

	// ---- S3. The passwordless account: the code opens the session, and
	// the passkey it enrolls cannot be taken away.

	silas := adminCreateUser(client, adminToken, "silas", "Silas")
	oneTimeCode = issueOneTimeCode(client, adminToken, silas)
	exchange = client.mustRPC("saka.authn.v1.OneTimeAccessService/ExchangeToken", map[string]any{
		"token": oneTimeCode,
	}, "")
	silasToken := str(exchange, "access_token")
	if silasToken == "" {
		fail("the exchange on the passwordless account answered no tokens: %v", exchange)
	}
	pass("the one-time code opened the passwordless account's session directly")

	silasDevice := softauthn.New(false, false, true)
	silasCredential := enroll(client, silasDevice, silasToken, "Silas key")

	if code := client.tryRPC("saka.authn.v1.WebAuthnService/AdminDeleteCredential", map[string]any{
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
	proofPasskey := mintPasskeyProof(client, vittoriaDevice, handleOf(vittoria), baseURL, vittoriaToken)
	pass("the step-up proofs minted, one by password and one by passkey")

	spendProof(client, vittoriaToken, proofPassword, sessionA)
	spendProof(client, vittoriaToken, proofPasskey, sessionB)
	pass("the two proofs spent independently")

	// ---- S5. The settings live: the toggle and the limit are judged at
	// enrollment, no restart in between.

	settingsUpdate(client, adminToken, "webauthn.allow_synced_passkeys", "false")
	syncedOptions, syncedSession := ceremony(client, "saka.authn.v1.WebAuthnService/BeginRegistration", vittoriaToken)
	synced := softauthn.New(true, true, true)
	if code := client.tryRPC("saka.authn.v1.WebAuthnService/VerifyRegistration", map[string]any{
		"session_id": syncedSession,
		"credential": mustCreate(synced, syncedOptions, baseURL),
		"name":       "Synced key",
	}, vittoriaToken); code != http.StatusBadRequest {
		fail("the synced enrollment under the off toggle answered %d", code)
	}
	settingsUpdate(client, adminToken, "webauthn.allow_synced_passkeys", "true")
	pass("the synced-passkey toggle is judged at enrollment")

	settingsUpdate(client, adminToken, "passkey.max_credentials", "1")
	limitOptions, limitSession := ceremony(client, "saka.authn.v1.WebAuthnService/BeginRegistration", vittoriaToken)
	if code := client.tryRPC("saka.authn.v1.WebAuthnService/VerifyRegistration", map[string]any{
		"session_id": limitSession,
		"credential": mustCreate(softauthn.New(false, false, true), limitOptions, baseURL),
		"name":       "Beyond the limit",
	}, vittoriaToken); code != http.StatusBadRequest {
		fail("the enrollment past the lowered limit answered %d", code)
	}
	settingsUpdate(client, adminToken, "passkey.max_credentials", "10")
	pass("the credential limit is judged at enrollment")

	// ---- S6. The lifecycle: rename by the holder, the admin doors over
	// another account, the delete-last allowed under a password.

	renamed := client.mustRPC("saka.authn.v1.WebAuthnService/UpdateCredential", map[string]any{
		"credential_id": enrolled,
		"name":          "Sophie's laptop",
	}, sophieToken)
	if str(renamed, "credential.name") != "Sophie's laptop" {
		fail("the rename answered the wrong name: %v", renamed)
	}
	pass("the holder renamed their passkey")

	adminRoll := client.mustRPC("saka.authn.v1.WebAuthnService/AdminListCredentials", map[string]any{
		"user_id": sophie,
	}, adminToken)
	adminList, _ := adminRoll["credentials"].([]any)
	if len(adminList) != 1 {
		fail("the admin roll carries %d credentials; sophie enrolled one", len(adminList))
	}
	client.mustRPC("saka.authn.v1.WebAuthnService/AdminUpdateCredential", map[string]any{
		"user_id":       sophie,
		"credential_id": enrolled,
		"name":          "Renamed by the operator",
	}, adminToken)
	client.mustRPC("saka.authn.v1.WebAuthnService/AdminDeleteCredential", map[string]any{
		"user_id":       sophie,
		"credential_id": enrolled,
	}, adminToken)
	pass("the admin doors listed, renamed, and removed sophie's passkey")

	// Delete-last is allowed: the password is the way back in. Sophie
	// re-enrolls to have one to lose, then loses it.
	second := enroll(client, device, sophieToken, "Second key")
	client.mustRPC("saka.authn.v1.WebAuthnService/DeleteCredential", map[string]any{
		"credential_id": second,
	}, sophieToken, "X-Saka-Reauthentication", reauthPassword(client, sophieToken, passwordOf("sophie")))
	pass("the delete-last allowed under a password")

	// ---- S7. The negative set.

	// A replayed assertion: the same body twice over the same, already
	// consumed ceremony handle — the second refused.
	options, sessionID := ceremony(client, "saka.authn.v1.WebAuthnService/BeginLogin", "")
	assertion := mustGet(vittoriaDevice, options, baseURL, handleOf(vittoria))
	if code := client.tryRPC("saka.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": assertion,
	}, ""); code != http.StatusOK {
		fail("the fresh assertion answered %d", code)
	}
	if code := client.tryRPC("saka.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": assertion,
	}, ""); code != http.StatusUnauthorized {
		fail("the replayed assertion answered %d", code)
	}
	pass("the replayed assertion refused")

	// An expired ceremony: a minute of silence, then the verify refuses.
	options, sessionID = ceremony(client, "saka.authn.v1.WebAuthnService/BeginLogin", "")
	time.Sleep(61 * time.Second)
	if code := client.tryRPC("saka.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": mustGet(vittoriaDevice, options, baseURL, handleOf(vittoria)),
	}, ""); code != http.StatusUnauthorized {
		fail("the expired ceremony answered %d", code)
	}
	pass("the expired ceremony refused")

	// An unknown credential: an authenticator sophie never enrolled.
	stranger := softauthn.New(false, false, true)
	options, sessionID = ceremony(client, "saka.authn.v1.WebAuthnService/BeginLogin", "")
	if code := client.tryRPC("saka.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": mustGet(stranger, options, baseURL, handleOf(sophie)),
	}, ""); code != http.StatusUnauthorized {
		fail("the unknown credential answered %d", code)
	}
	pass("the unknown credential refused")

	// A banned account: sophie's passkey is real, the account is closed.
	client.mustRPC("saka.identity.v1.UserService/BanUser", map[string]any{
		"id":     sophie,
		"reason": "the E2E ladder is holding the door",
	}, adminToken)
	options, sessionID = ceremony(client, "saka.authn.v1.WebAuthnService/BeginLogin", "")
	if code := client.tryRPC("saka.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": mustGet(device, options, baseURL, handleOf(sophie)),
	}, ""); code != http.StatusUnauthorized {
		fail("the banned account answered %d", code)
	}
	client.mustRPC("saka.identity.v1.UserService/UnbanUser", map[string]any{
		"id": sophie,
	}, adminToken)
	pass("the banned account refused, then unbanned")

	// A cloned credential: the counter rewinds, the refusal is silent.
	vittoriaDevice.RewindCounter()
	options, sessionID = ceremony(client, "saka.authn.v1.WebAuthnService/BeginLogin", "")
	if code := client.tryRPC("saka.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": mustGet(vittoriaDevice, options, baseURL, handleOf(vittoria)),
	}, ""); code != http.StatusBadRequest {
		fail("the cloned credential answered %d", code)
	}
	pass("the cloned credential refused")

	fmt.Println("status: the passkey ladder ran clean")
}
