package main

// The remaining surfaces the ladder touches: the one-time code issue and
// the admin settings write.

// issueOneTimeCode issues a code for one account and answers it.
func issueOneTimeCode(client *rpcClient, adminToken, userWire string) string {
	issued := client.mustRPC("saka.authn.v1.OneTimeAccessService/CreateToken", map[string]any{
		"id": userWire,
	}, adminToken)
	code := str(issued, "token")
	if code == "" {
		fail("the one-time issue answered no code: %v", issued)
	}
	return code
}

// settingsUpdate writes one setting through the admin surface.
func settingsUpdate(client *rpcClient, adminToken, key, value string) {
	client.mustRPC("saka.settings.v1.SettingsService/Update", map[string]any{
		"key":   key,
		"value": value,
	}, adminToken)
}
