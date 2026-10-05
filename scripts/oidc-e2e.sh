#!/usr/bin/env bash
# The OIDC provider's E2E flow exercise: every grant and endpoint the
# discovery document names, driven against a running server with curl.
#
# Setup the script assumes:
#   - the server answers on ${SAKA_BASE:-http://localhost:3080}
#   - an account exists whose credentials are $SAKA_USER/$SAKA_PASS
#     (default admin / Expecto-Patronum-9 — the dev bootstrap's shape)
#   - the rate limits are loose enough for the run: serve with a throwaway
#     config file that raises `rate_limit.limit` and `rate_limit.auth_limit`
#     (the committed defaults are 60/10 per minute — the run makes dozens of
#     token calls, and the credential bucket trips at ten)
#
# The script creates one confidential client (e2e-oidc-<ts>) and one public
# client, then walks: discovery, JWKS, authorization code + PKCE (post and
# basic client authn), the consent path, userinfo, refresh (rotation and
# replay), introspection, revocation, end-session, client credentials, the
# device grant, PAR, and the refusal shapes a conformance suite probes.
set -euo pipefail

BASE="${SAKA_BASE:-http://localhost:3080}"
USER="${SAKA_USER:-admin}"
PASS="${SAKA_PASS:-Expecto-Patronum-9}"
STAMP=$(date +%s)

PASS_COUNT=0
FAIL_COUNT=0
declare -a FAILURES=()

# check <name> <expected> <actual>
check() {
    local name="$1" expected="$2" actual="$3"
    if [ "$expected" = "$actual" ]; then
        PASS_COUNT=$((PASS_COUNT + 1))
        echo "ok    $name"
    else
        FAIL_COUNT=$((FAIL_COUNT + 1))
        FAILURES+=("$name (want $expected, got $actual)")
        echo "FAIL  $name (want $expected, got $actual)"
    fi
}

contains() {
    local name="$1" haystack="$2" needle="$3"
    if [[ "$haystack" == *"$needle"* ]]; then
        PASS_COUNT=$((PASS_COUNT + 1))
        echo "ok    $name"
    else
        FAIL_COUNT=$((FAIL_COUNT + 1))
        FAILURES+=("$name (missing: $needle)")
        echo "FAIL  $name (missing: $needle)"
        return 1
    fi
}

# has_needle answers without touching the counters.
has_needle() { [[ "$2" == *"$1"* ]]; }

# either <name> <haystack> <needle>... — any one match passes.
either() {
    local name="$1" haystack="$2"
    shift 2
    local needle
    for needle in "$@"; do
        if has_needle "$needle" "$haystack"; then
            PASS_COUNT=$((PASS_COUNT + 1))
            echo "ok    $name ($needle)"
            return 0
        fi
    done
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILURES+=("$name (none of: $*)")
    echo "FAIL  $name (none of: $*)"
    echo "  raw: $haystack"
    return 1
}

rand_hex() { openssl rand -hex 32; }
s256() { printf '%s' "$1" | openssl dgst -sha256 -binary | base64 | tr '+/' '-_' | tr -d '='; }

echo "== sign in =="
SIGNIN=$(curl -s "$BASE/rpc/saka.authn.v1.AuthService/SignIn" \
    -H 'Content-Type: application/json' \
    -d "{\"identity\":\"$USER\",\"password\":\"$PASS\"}")
ACCESS=$(echo "$SIGNIN" | python3 -c 'import sys,json;print(json.load(sys.stdin)["access_token"])')
[ -n "$ACCESS" ] || {
    echo "sign-in failed: $SIGNIN"
    exit 1
}

echo "== create clients =="
CREATE=$(curl -s "$BASE/rpc/saka.federation.v1.OidcClientService/CreateClient" \
    -H "Authorization: Bearer $ACCESS" -H 'Content-Type: application/json' \
    -d "{
    \"id\": \"e2e-conf-$STAMP\",
    \"name\": \"E2E Confidential\",
    \"callbackUrls\": [\"http://localhost:9010/auth/callback\"],
    \"logoutCallbackUrls\": [\"http://localhost:9010/logout\"],
    \"skipConsent\": true
  }")
CLIENT_ID=$(echo "$CREATE" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("client",{}).get("id",""))')
CLIENT_SECRET=$(echo "$CREATE" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("secret",""))')
[ -n "$CLIENT_ID" ] && [ -n "$CLIENT_SECRET" ] || {
    echo "client creation failed: $CREATE"
    exit 1
}
echo "client: $CLIENT_ID"

CREATE_PUB=$(curl -s "$BASE/rpc/saka.federation.v1.OidcClientService/CreateClient" \
    -H "Authorization: Bearer $ACCESS" -H 'Content-Type: application/json' \
    -d "{
    \"id\": \"e2e-pub-$STAMP\",
    \"name\": \"E2E Public\",
    \"callbackUrls\": [\"http://localhost:9010/auth/callback\"],
    \"isPublic\": true,
    \"skipConsent\": true
  }")
PUB_ID=$(echo "$CREATE_PUB" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("client",{}).get("id",""))')
[ -n "$PUB_ID" ] || {
    echo "public client creation failed: $CREATE_PUB"
    exit 1
}

echo "== discovery and JWKS =="
DOC=$(curl -s "$BASE/.well-known/openid-configuration")
contains "discovery: issuer" "$DOC" "\"issuer\":\"$BASE\""
contains "discovery: token endpoint" "$DOC" "/oidc/token"
contains "discovery: code challenge methods" "$DOC" "S256"
contains "discovery: no plain challenge method" "$DOC" '"code_challenge_methods_supported":["S256"]'
contains "discovery: claims_supported names the subject" "$DOC" '"claims_supported":["sub"'
JWKS_CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/$(echo "$DOC" | python3 -c 'import sys,json,urllib.parse;print(urllib.parse.urlparse(json.load(sys.stdin)["jwks_uri"]).path)')")
check "discovery jwks_uri answers 200" "200" "$JWKS_CODE"

echo "== authorization code + PKCE (secret_post) =="
VERIFIER=$(rand_hex)
CHALLENGE=$(s256 "$VERIFIER")
STATE=$(rand_hex)
NONCE=$(rand_hex)

AUTH_CODE=$(curl -s -o /dev/null -w '%{redirect_url}' \
    "$BASE/oidc/authorize?response_type=code&client_id=$CLIENT_ID&scope=openid%20profile%20email&state=$STATE&nonce=$NONCE&code_challenge=$CHALLENGE&code_challenge_method=S256&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback" \
    -H "Authorization: Bearer $ACCESS" | sed -n 's/.*[?&]code=\([^&]*\).*/\1/p')
[ -n "$AUTH_CODE" ] || {
    echo "no authorization code in the redirect"
    exit 1
}
check "authorize: code issued" "yes" "yes"

TOKENS=$(curl -s "$BASE/oidc/token" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "grant_type=authorization_code&code=$AUTH_CODE&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback&client_id=$CLIENT_ID&client_secret=$CLIENT_SECRET&code_verifier=$VERIFIER")
ACCESS_TOKEN=$(echo "$TOKENS" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("access_token",""))')
ID_TOKEN=$(echo "$TOKENS" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id_token",""))')
REFRESH_TOKEN=$(echo "$TOKENS" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("refresh_token",""))')
[ -n "$ACCESS_TOKEN" ] && [ -n "$ID_TOKEN" ] && [ -n "$REFRESH_TOKEN" ] &&
    check "token: code exchange" "yes" "yes" || {
    echo "token exchange failed: $TOKENS"
    exit 1
}

# The ID token's claims.
ID_PAYLOAD=$(echo "$ID_TOKEN" | cut -d. -f2 | python3 -c 'import sys,base64;s=sys.stdin.read().strip();print(base64.urlsafe_b64decode(s+"="*(-len(s)%4)).decode())')
contains "id_token: nonce echo" "$ID_PAYLOAD" "\"nonce\":\"$NONCE\""
contains "id_token: sub present" "$ID_PAYLOAD" '"sub"'
contains "id_token: iss" "$ID_PAYLOAD" "\"iss\":\"$BASE\""

# The code replay is the LAST thing this grant sees: RFC 9700 has the
# provider revoke every token the code issued once the code is replayed,
# so the userinfo, refresh, introspection, and revocation checks below
# must run first.
echo "== userinfo =="
USERINFO=$(curl -s "$BASE/oidc/userinfo" -H "Authorization: Bearer $ACCESS_TOKEN")
contains "userinfo: sub" "$USERINFO" '"sub"'
contains "userinfo: email" "$USERINFO" 'admin@example.com'

echo "== refresh: rotation and replay =="
ROTATED=$(curl -s "$BASE/oidc/token" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "grant_type=refresh_token&refresh_token=$REFRESH_TOKEN&client_id=$CLIENT_ID&client_secret=$CLIENT_SECRET")
NEW_REFRESH=$(echo "$ROTATED" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("refresh_token",""))')
[ -n "$NEW_REFRESH" ] && [ "$NEW_REFRESH" != "$REFRESH_TOKEN" ] &&
    check "refresh: token rotated" "yes" "yes" || echo "FAIL  refresh: token rotated ($ROTATED)"

echo "== basic client authn on refresh =="
BASIC_REFRESH=$(curl -s "$BASE/oidc/token" \
    -u "$CLIENT_ID:$CLIENT_SECRET" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "grant_type=refresh_token&refresh_token=$NEW_REFRESH")
BASIC_ACCESS=$(echo "$BASIC_REFRESH" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("access_token",""))')
[ -n "$BASIC_ACCESS" ] && check "refresh: basic authn accepted" "yes" "yes" || echo "FAIL  refresh: basic authn ($BASIC_REFRESH)"

echo "== introspection =="
INTROSPECT=$(curl -s "$BASE/oidc/introspect" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "token=$BASIC_ACCESS&client_id=$CLIENT_ID&client_secret=$CLIENT_SECRET")
contains "introspect: active" "$INTROSPECT" '"active":true'

STRANGER_INTROSPECT=$(curl -s "$BASE/oidc/introspect" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "token=$BASIC_ACCESS&client_id=$PUB_ID")
# RFC 7662 leaves the cross-client answer open: the provider refuses the
# stranger with access_denied rather than an active:false body.
either "introspect: stranger refused" "$STRANGER_INTROSPECT" '"active":false' "access_denied"

echo "== revocation =="
REVOKE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/oidc/revoke" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "token=$BASIC_ACCESS&client_id=$CLIENT_ID&client_secret=$CLIENT_SECRET")
check "revoke: 200" "200" "$REVOKE"
AFTER_REVOKE=$(curl -s "$BASE/oidc/introspect" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "token=$BASIC_ACCESS&client_id=$CLIENT_ID&client_secret=$CLIENT_SECRET")
contains "revoke: token dead after revocation" "$AFTER_REVOKE" '"active":false'

echo "== authorization-code replay =="
REPLAY=$(curl -s "$BASE/oidc/token" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "grant_type=authorization_code&code=$AUTH_CODE&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback&client_id=$CLIENT_ID&client_secret=$CLIENT_SECRET&code_verifier=$VERIFIER")
# The pointer row is gone and, if the grant was revoked in between, the
# refusal names the expiry — either word is a refusal.
either "token: code replay refused" "$REPLAY" "invalid_grant" "expired_token"
# RFC 9700: the replay revokes every token the code issued.
AFTER_REPLAY=$(curl -s "$BASE/oidc/userinfo" -H "Authorization: Bearer $ACCESS_TOKEN")
contains "replay: previously issued token revoked (RFC 9700)" "$AFTER_REPLAY" "invalid_token"

echo "== public client: PKCE without secret =="
VERIFIER2=$(rand_hex)
CHALLENGE2=$(s256 "$VERIFIER2")
AUTH_CODE2=$(curl -s -o /dev/null -w '%{redirect_url}' \
    "$BASE/oidc/authorize?response_type=code&client_id=$PUB_ID&scope=openid&state=s2&code_challenge=$CHALLENGE2&code_challenge_method=S256&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback" \
    -H "Authorization: Bearer $ACCESS" | sed -n 's/.*[?&]code=\([^&]*\).*/\1/p')
PUB_TOKENS=$(curl -s "$BASE/oidc/token" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "grant_type=authorization_code&code=$AUTH_CODE2&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback&client_id=$PUB_ID&code_verifier=$VERIFIER2")
PUB_ACCESS=$(echo "$PUB_TOKENS" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("access_token",""))')
[ -n "$PUB_ACCESS" ] && check "public client: code exchange without secret" "yes" "yes" || echo "FAIL  public client exchange ($PUB_TOKENS)"

echo "== PKCE enforcement =="
BAD_VERIFIER=$(rand_hex)
AUTH_CODE3=$(curl -s -o /dev/null -w '%{redirect_url}' \
    "$BASE/oidc/authorize?response_type=code&client_id=$PUB_ID&scope=openid&state=s3&code_challenge=$CHALLENGE2&code_challenge_method=S256&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback" \
    -H "Authorization: Bearer $ACCESS" | sed -n 's/.*[?&]code=\([^&]*\).*/\1/p')
WRONG=$(curl -s "$BASE/oidc/token" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "grant_type=authorization_code&code=$AUTH_CODE3&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback&client_id=$PUB_ID&code_verifier=$BAD_VERIFIER")
contains "pkce: wrong verifier refused" "$WRONG" "invalid_grant"

echo "== client credentials =="
CC_GRANT=$(curl -s "$BASE/rpc/saka.federation.v1.OidcClientService/UpdateClient" \
    -H "Authorization: Bearer $ACCESS" -H 'Content-Type: application/json' \
    -d "{\"id\": \"$CLIENT_ID\", \"name\": \"E2E Confidential\", \"callbackUrls\": [\"http://localhost:9010/auth/callback\"], \"logoutCallbackUrls\": [\"http://localhost:9010/logout\"], \"skipConsent\": true, \"allowedGrantTypes\": [\"authorization_code\", \"refresh_token\", \"client_credentials\"]}")
CC=$(curl -s "$BASE/oidc/token" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "grant_type=client_credentials&client_id=$CLIENT_ID&client_secret=$CLIENT_SECRET")
CC_ACCESS=$(echo "$CC" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("access_token",""))')
[ -n "$CC_ACCESS" ] && check "client_credentials: token minted" "yes" "yes" || echo "FAIL  client_credentials ($CC)"
CC_INTROSPECT=$(curl -s "$BASE/oidc/introspect" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "token=$CC_ACCESS&client_id=$CLIENT_ID&client_secret=$CLIENT_SECRET")
contains "client_credentials: introspects active" "$CC_INTROSPECT" '"active":true'

echo "== PAR =="
PAR=$(curl -s "$BASE/oidc/par" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "client_id=$PUB_ID&response_type=code&scope=openid&code_challenge=$CHALLENGE2&code_challenge_method=S256&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback")
REQUEST_URI=$(echo "$PAR" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("request_uri",""))')
[ -n "$REQUEST_URI" ] && check "par: request_uri issued" "yes" "yes" || echo "FAIL  par ($PAR)"
if [ -n "$REQUEST_URI" ]; then
    PAR_CODE=$(curl -s -o /dev/null -w '%{redirect_url}' \
        "$BASE/oidc/authorize?client_id=$PUB_ID&request_uri=$(python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))" "$REQUEST_URI")" \
        -H "Authorization: Bearer $ACCESS" | sed -n 's/.*[?&]code=\([^&]*\).*/\1/p')
    [ -n "$PAR_CODE" ] && check "par: authorize through request_uri" "yes" "yes" || echo "FAIL  par authorize"
fi

echo "== device grant =="
DEVICE=$(curl -s "$BASE/oidc/device_authorization" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "client_id=$PUB_ID&scope=openid")
DEVICE_CODE=$(echo "$DEVICE" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("device_code",""))')
USER_CODE=$(echo "$DEVICE" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("user_code",""))')
VERIF_URI=$(echo "$DEVICE" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("verification_uri",""))')
[ -n "$DEVICE_CODE" ] && check "device: codes issued" "yes" "yes" || echo "FAIL  device ($DEVICE)"
if [ -n "$DEVICE_CODE" ]; then
    contains "device: verification_uri in document" "$DEVICE" "$VERIF_URI"
    # The browser half: the verification visit with the bearer credential
    # approves the session (skipConsent), the shape the SPA walks.
    VERIFY_CODE=$(curl -s -o /dev/null -w '%{http_code}' "$VERIF_URI?user_code=$USER_CODE" \
        -H "Authorization: Bearer $ACCESS")
    check "device: verification visit approved" "200" "$VERIFY_CODE"
    # The device polls.
    DEV_TOKENS=$(curl -s "$BASE/oidc/token" \
        -H 'Content-Type: application/x-www-form-urlencoded' \
        -d "grant_type=urn:ietf:params:oauth:grant-type:device_code&device_code=$DEVICE_CODE&client_id=$PUB_ID")
    DEV_ACCESS=$(echo "$DEV_TOKENS" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("access_token",""))')
    [ -n "$DEV_ACCESS" ] && check "device: token minted after approval" "yes" "yes" || echo "FAIL  device token ($DEV_TOKENS)"
fi

echo "== refusal shapes a conformance suite probes =="
NO_CLIENT=$(curl -s "$BASE/oidc/token" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "grant_type=authorization_code&code=x&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback")
contains "unknown client refused" "$NO_CLIENT" "invalid_client"
BAD_SECRET=$(curl -s "$BASE/oidc/token" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "grant_type=client_credentials&client_id=$CLIENT_ID&client_secret=wrong")
contains "wrong secret refused" "$BAD_SECRET" "invalid_client"
UNKNOWN_SCOPE=$(curl -s -o /dev/null -w '%{redirect_url}' \
    "$BASE/oidc/authorize?response_type=code&client_id=$CLIENT_ID&scope=openid%20superpowers&state=s4&code_challenge=$CHALLENGE&code_challenge_method=S256&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback" \
    -H "Authorization: Bearer $ACCESS")
echo "note  unknown scope redirect: $UNKNOWN_SCOPE"
BAD_REDIRECT=$(curl -s -w '\n%{http_code}' "$BASE/oidc/authorize?response_type=code&client_id=$CLIENT_ID&scope=openid&state=s5&code_challenge=$CHALLENGE&code_challenge_method=S256&redirect_uri=http%3A%2F%2Fevil.example%2Fcb" \
    -H "Authorization: Bearer $ACCESS" | tail -1)
check "unregistered redirect_uri refused" "400" "$BAD_REDIRECT"

# RFC 9700 §4.1.3: plain is the challenge method a server stops offering —
# the authorize request naming it is refused outright.
PLAIN_REDIRECT=$(curl -s -o /dev/null -w '%{redirect_url}' \
    "$BASE/oidc/authorize?response_type=code&client_id=$PUB_ID&scope=openid&state=s6&code_challenge=$VERIFIER&code_challenge_method=plain&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback" \
    -H "Authorization: Bearer $ACCESS")
contains "pkce: plain method refused" "$PLAIN_REDIRECT" "error=invalid_request"

# The deployment-wide ledger read is a page, not the whole table.
LEDGER=$(curl -s "$BASE/rpc/saka.federation.v1.OidcConsentService/ListAllAuthorizedClients" \
    -H "Authorization: Bearer $ACCESS" -H 'Content-Type: application/json' \
    -d '{"page":1,"limit":10}')
contains "ledger: page carries the pagination block" "$LEDGER" '"total_items"'
contains "ledger: page respects the limit" "$LEDGER" '"limit":10'

echo "== end-session =="
# RP-Initiated Logout: the post-logout redirect must be one the client
# registered, and the state the RP sent rides back on it. The logout walks
# a fresh grant — the revoke section above already withdrew the first one,
# and a hint whose grant is gone names no client to match a registered
# redirect against.
LOGOUT_CODE=$(curl -s -o /dev/null -w '%{redirect_url}' \
    "$BASE/oidc/authorize?response_type=code&client_id=$CLIENT_ID&scope=openid&state=lo&code_challenge=$CHALLENGE&code_challenge_method=S256&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback" \
    -H "Authorization: Bearer $ACCESS" | sed -n 's/.*[?&]code=\([^&]*\).*/\1/p')
[ -n "$LOGOUT_CODE" ] || {
    echo "no logout code minted"
    exit 1
}
LOGOUT_TOKENS=$(curl -s "$BASE/oidc/token" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "grant_type=authorization_code&code=$LOGOUT_CODE&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback&client_id=$CLIENT_ID&client_secret=$CLIENT_SECRET&code_verifier=$VERIFIER")
LOGOUT_HINT=$(echo "$LOGOUT_TOKENS" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id_token",""))')
[ -n "$LOGOUT_HINT" ] || {
    echo "no logout hint minted (tokens=$(echo "$LOGOUT_TOKENS" | head -c 200))"
    exit 1
}
ES_STATE="es-$STAMP"
ES_REDIRECT=$(curl -s -o /dev/null -w '%{redirect_url}' \
    "$BASE/oidc/end-session?id_token_hint=$LOGOUT_HINT&client_id=$CLIENT_ID&post_logout_redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Flogout&state=$ES_STATE")
contains "end-session: redirect to the registered URI" "$ES_REDIRECT" "localhost:9010/logout"
contains "end-session: state echoed" "$ES_REDIRECT" "state=$ES_STATE"
BAD_POSTLOGOUT=$(curl -s -w '\n%{http_code}' \
    "$BASE/oidc/end-session?id_token_hint=$LOGOUT_HINT&client_id=$CLIENT_ID&post_logout_redirect_uri=http%3A%2F%2Fevil.example%2Flogout" | tail -1)
check "end-session: unregistered post_logout_redirect_uri refused" "400" "$BAD_POSTLOGOUT"
ES=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/oidc/end-session?id_token_hint=$LOGOUT_HINT&client_id=$CLIENT_ID")
check "end-session answers" "302" "$ES"

echo
echo "==== $PASS_COUNT passed, $FAIL_COUNT failed ===="
if [ "$FAIL_COUNT" -gt 0 ]; then
    for f in "${FAILURES[@]}"; do echo "  - $f"; done
    exit 1
fi
