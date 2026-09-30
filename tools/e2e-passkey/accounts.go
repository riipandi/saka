package main

import "strings"

// The account surfaces the ladder walks: the signups, the password sign-ins
// and their MFA fork, and the session list the guarded calls act on.

func passwordOf(username string) string { return "@" + username + "-passkey-e2e" }

// signup creates the account under an admin-issued signup token and answers
// the account's wire form.
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

// adminCreateUser creates an account that carries no password — the state a
// one-time code serves.
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

// passwordSignIn signs in with the password on an account that owes no
// second factor, and answers the access token.
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

// currentSession answers the caller's newest live session — the list
// includes the ended ones, so the pick reads the revoked_at absence.
func currentSession(client *rpcClient, accessToken string) string {
	sessions := client.mustRPC("tango.authn.v1.SessionService/ListSessions", map[string]any{}, accessToken)
	list, _ := sessions["sessions"].([]any)
	for _, entry := range list {
		row, _ := entry.(map[string]any)
		if row == nil {
			continue
		}
		if _, ended := row["revoked_at"]; ended {
			continue
		}
		if id, _ := row["id"].(string); id != "" {
			return id
		}
	}
	fail("the session list carries no live session: %v", sessions)
	return ""
}
