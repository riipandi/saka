package main

import (
	"github.com/riipandi/saka/modules/identity/user"
	"github.com/riipandi/saka/pkg/testutils/softauthn"
)

// The WebAuthn ceremonies: registration, the discoverable sign-in, and the
// shapes the browser would hand the SPA — passed through untouched.

// ceremony opens a ceremony and answers the options document and the session
// handle the verify call echoes.
func ceremony(client *rpcClient, procedure, accessToken string) (options, sessionID string) {
	answer := client.mustRPC(procedure, map[string]any{}, accessToken)
	options = str(answer, "options")
	sessionID = str(answer, "session_id")
	if options == "" || sessionID == "" {
		fail("%s answered an incomplete ceremony: %v", procedure, answer)
	}
	return options, sessionID
}

// enroll walks the registration ceremony and answers the enrolled
// credential's wire identifier.
func enroll(client *rpcClient, device *softauthn.Authenticator, accessToken, name string) string {
	options, sessionID := ceremony(client, "saka.authn.v1.WebAuthnService/BeginRegistration", accessToken)
	verified := client.mustRPC("saka.authn.v1.WebAuthnService/VerifyRegistration", map[string]any{
		"session_id": sessionID,
		"credential": mustCreate(device, options, client.base),
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
	begun := client.mustRPC("saka.authn.v1.MultifactorService/BeginTotpEnrollment", map[string]any{
		"name": "E2E device",
	}, accessToken)
	totpID, secret = str(begun, "totp_id"), str(begun, "secret")
	if totpID == "" || secret == "" {
		fail("the TOTP enrollment began without a secret: %v", begun)
	}
	confirmed := client.mustRPC("saka.authn.v1.MultifactorService/ConfirmTotpEnrollment", map[string]any{
		"totp_id": totpID,
		"code":    totpCode(secret, timeNow()),
	}, accessToken)
	codes, _ := confirmed["recovery_codes"].([]any)
	for _, entry := range codes {
		code, _ := entry.(string)
		recoveryCodes = append(recoveryCodes, code)
	}
	if len(recoveryCodes) == 0 {
		fail("the TOTP confirm answered no recovery codes: %v", confirmed)
	}
	return totpID, secret, recoveryCodes
}

// passwordlessSignIn signs in by the passkey alone and fails the run unless
// the whole session issued.
func passwordlessSignIn(client *rpcClient, device *softauthn.Authenticator, handle []byte) {
	options, sessionID := ceremony(client, "saka.authn.v1.WebAuthnService/BeginLogin", "")
	answer := client.mustRPC("saka.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": mustGet(device, options, client.base, handle),
	}, "")
	if str(answer, "access_token") == "" {
		fail("the passkey sign-in answered no access token: %v", answer)
	}
}

// passwordlessSession signs in by the passkey alone and answers the opened
// session's identifier — the shape a revoke target takes.
func passwordlessSession(client *rpcClient, device *softauthn.Authenticator, handle []byte) string {
	options, sessionID := ceremony(client, "saka.authn.v1.WebAuthnService/BeginLogin", "")
	answer := client.mustRPC("saka.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": mustGet(device, options, client.base, handle),
	}, "")
	id := str(answer, "session_id")
	if id == "" {
		fail("the passkey sign-in answered no session id: %v", answer)
	}
	return id
}

// completeSignInCode finishes the bridge with the authenticator's code — a
// TOTP value or one of the account's recovery codes.
func completeSignInCode(client *rpcClient, pending, code string) {
	client.mustRPC("saka.authn.v1.MultifactorService/CompleteSignIn", map[string]any{
		"pending_token": pending,
		"code":          code,
	}, "")
}

// completeSignInPasskey finishes the bridge with the passkey assertion.
func completeSignInPasskey(client *rpcClient, pending string, device *softauthn.Authenticator, handle []byte, origin string) {
	options, sessionID := ceremony(client, "saka.authn.v1.WebAuthnService/BeginLogin", "")
	client.mustRPC("saka.authn.v1.MultifactorService/CompleteSignIn", map[string]any{
		"pending_token": pending,
		"passkey": map[string]string{
			"session_id": sessionID,
			"credential": mustGet(device, options, origin, handle),
		},
	}, "")
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
