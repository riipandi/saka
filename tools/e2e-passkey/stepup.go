package main

import (
	"github.com/riipandi/tango/pkg/testutils/softauthn"
)

// The step-up: proofs minted by Reauthenticate, spent by guarded calls.

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

// reauthPassword mints the step-up proof the guarded delete needs.
func reauthPassword(client *rpcClient, accessToken, password string) string {
	return mintProof(client, map[string]any{"password": password}, accessToken)
}

// spendProof spends the header proof on the guarded revocation and holds
// the replay to the unauthenticated refusal.
func spendProof(client *rpcClient, accessToken, proof, sessionID string) {
	client.mustRPC("tango.authn.v1.SessionService/RevokeSession", map[string]any{
		"id": sessionID,
	}, accessToken, "X-Tango-Reauthentication", proof)

	if code := client.tryRPC("tango.authn.v1.SessionService/RevokeSession", map[string]any{
		"id": sessionID,
	}, accessToken, "X-Tango-Reauthentication", proof); code != 401 {
		fail("the spent proof answered %d; the refusal must be unauthenticated", code)
	}
}
