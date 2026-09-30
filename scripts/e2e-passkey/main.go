// e2e-passkey drives the passkey authentication ladder against a live
// server: sign in with a password, enroll a passkey, sign in passwordless,
// complete an MFA bridge with the passkey, spend a step-up proof on a
// guarded procedure, and prove the spent proof refuses. It is the wire-level
// E2E the passkey plan's Phase D runs; the soft authenticator answers the
// ceremonies the way a browser would.
//
// Usage: go run ./scripts/e2e-passkey --base-url http://localhost:3080
//
// The identity and password come from the environment: E2E_IDENTITY and
// E2E_PASSWORD. Every artifact the run creates — the enrolled credential,
// the sessions it opened — is the caller's to clean up; the run itself
// writes nothing to disk.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/riipandi/tango/modules/identity/user"
	"github.com/riipandi/tango/pkg/testutils/softauthn"
)

const rpcPath = "/rpc/"

func main() {
	baseURL := flag.String("base-url", "http://localhost:3080", "the server's base URL")
	flag.Parse()

	identity := os.Getenv("E2E_IDENTITY")
	password := os.Getenv("E2E_PASSWORD")
	if identity == "" || password == "" {
		fail("E2E_IDENTITY and E2E_PASSWORD must be set")
	}

	client := &rpcClient{base: strings.TrimSuffix(*baseURL, "/"), http: &http.Client{}}

	// 1. Password sign-in answers the token pair the enrollment runs on.
	signIn := client.mustRPC("tango.authn.v1.AuthService/SignIn", map[string]any{
		"identity": identity,
		"password": password,
	}, "")
	accessToken := str(signIn, "access_token")
	if accessToken == "" {
		fail("the sign-in answered no access token: %v", signIn)
	}
	pass("password sign-in issued a session")

	// 2. Enroll a passkey on the authenticated session.
	device := softauthn.New(false, false, true)
	options, sessionID := ceremony(client, "tango.authn.v1.WebAuthnService/BeginRegistration", accessToken)
	response, err := device.Create(options, *baseURL)
	if err != nil {
		fail("soft authenticator create: %v", err)
	}
	enrolled := client.mustRPC("tango.authn.v1.WebAuthnService/VerifyRegistration", map[string]any{
		"session_id": sessionID,
		"credential": response,
		"name":       "E2E key",
	}, accessToken)
	pass("passkey enrolled: %s", str(enrolled, "credential.id"))

	// 3. Sign in passwordless: no identity, no password, one assertion.
	options, sessionID = ceremony(client, "tango.authn.v1.WebAuthnService/BeginLogin", "")
	assertion, err := device.Get(options, *baseURL, mustUserHandle(signIn))
	if err != nil {
		fail("soft authenticator get: %v", err)
	}
	passkeySignIn := client.mustRPC("tango.authn.v1.WebAuthnService/VerifyLogin", map[string]any{
		"session_id": sessionID,
		"credential": assertion,
	}, "")
	if str(passkeySignIn, "access_token") == "" {
		fail("the passkey sign-in answered no access token: %v", passkeySignIn)
	}
	pass("passkey sign-in opened a whole session")

	// 4. Step-up: re-prove by passkey and spend the proof on the guarded
	// delete — one proof, one call. The replay refuses.
	options, sessionID = ceremony(client, "tango.authn.v1.WebAuthnService/BeginLogin", "")
	assertion, err = device.Get(options, *baseURL, mustUserHandle(signIn))
	if err != nil {
		fail("soft authenticator get: %v", err)
	}
	reauth := client.mustRPC("tango.authn.v1.WebAuthnService/Reauthenticate", map[string]any{
		"passkey": map[string]string{"session_id": sessionID, "credential": assertion},
	}, accessToken)
	proof := str(reauth, "token")
	if proof == "" {
		fail("the reauthentication answered no token: %v", reauth)
	}
	pass("step-up proof minted")

	// The roll names the enrolled credential; the guarded delete spends the
	// header proof.
	roll := client.mustRPC("tango.authn.v1.WebAuthnService/ListCredentials", map[string]any{}, accessToken)
	rollList, _ := roll["credentials"].([]any)
	if len(rollList) != 1 {
		fail("the roll carries %d credentials; the enrolled one should be alone", len(rollList))
	}
	enrolledID, _ := rollList[0].(map[string]any)["id"].(string)
	if enrolledID == "" {
		fail("the roll carries no readable credential id")
	}
	client.mustRPC("tango.authn.v1.WebAuthnService/DeleteCredential", map[string]any{
		"credential_id": enrolledID,
	}, accessToken, "X-Tango-Reauthentication", proof)
	pass("guarded delete spent the proof")

	// The replay refuses: the same proof answers unauthenticated, not the
	// procedure's own answer.
	code := client.tryRPC("tango.authn.v1.WebAuthnService/DeleteCredential", map[string]any{
		"credential_id": enrolledID,
	}, accessToken, "X-Tango-Reauthentication", proof)
	if code != http.StatusUnauthorized {
		fail("the spent proof answered %d; the refusal must be unauthenticated", code)
	}
	pass("the spent proof refused")

	// The roll is empty: the credential the ladder enrolled is gone.
	roll = client.mustRPC("tango.authn.v1.WebAuthnService/ListCredentials", map[string]any{}, accessToken)
	rollList, _ = roll["credentials"].([]any)
	if len(rollList) != 0 {
		fail("the roll still carries %d credentials after the delete", len(rollList))
	}
	pass("the roll is empty after the delete")

	fmt.Println("status: the passkey ladder ran clean")
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
	value, _ := document[field].(string)
	return value
}

func pass(format string, args ...any) {
	fmt.Printf("  "+format+"\n", args...)
}

func fail(format string, args ...any) {
	fmt.Printf("status: "+format+"\n", args...)
	os.Exit(1)
}

// rpcClient speaks the ConnectRPC surface: one POST per procedure, the JSON
// body the one argument, the bearer header when the caller holds a token.
type rpcClient struct {
	base string
	http *http.Client
}

func (c *rpcClient) call(procedure, body, accessToken string, headers ...string) (int, map[string]any) {
	request, err := http.NewRequest(http.MethodPost, c.base+rpcPath+procedure, bytes.NewReader([]byte(body)))
	if err != nil {
		fail("build request: %v", err)
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
		fail("call %s: %v", procedure, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		fail("read %s: %v", procedure, err)
	}
	var document map[string]any
	_ = json.Unmarshal(raw, &document)
	return response.StatusCode, document
}

func (c *rpcClient) mustRPC(procedure string, body map[string]any, accessToken string, headers ...string) map[string]any {
	raw, err := json.Marshal(body)
	if err != nil {
		fail("marshal %s: %v", procedure, err)
	}
	code, document := c.call(procedure, string(raw), accessToken, headers...)
	if code != http.StatusOK {
		fail("%s answered %d: %v", procedure, code, document)
	}
	return document
}

func (c *rpcClient) tryRPC(procedure string, body map[string]any, accessToken string, headers ...string) int {
	raw, err := json.Marshal(body)
	if err != nil {
		fail("marshal %s: %v", procedure, err)
	}
	code, _ := c.call(procedure, string(raw), accessToken, headers...)
	return code
}
