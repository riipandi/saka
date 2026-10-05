#!/usr/bin/env bash
# The concurrency probe: N parallel requests race for one authorization
# code and one refresh token. The provider's compare-and-swap must answer
# exactly one winner per round.
set -euo pipefail
BASE="${SAKA_BASE:-http://localhost:3080}"
ADMIN=$(cat /tmp/saka_token.txt)
STAMP=$(date +%s)

CREATE=$(curl -s "$BASE/rpc/saka.federation.v1.OidcClientService/CreateClient" \
    -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
    -d "{\"id\":\"race-$STAMP\",\"name\":\"Race\",\"callbackUrls\":[\"http://localhost:9010/auth/callback\"],\"skipConsent\":true,\"isPublic\":true}")
CID=$(echo "$CREATE" | python3 -c 'import sys,json;print(json.load(sys.stdin)["client"]["id"])')

mk_code() {
    local V C LOC
    V=$(openssl rand -hex 32)
    C=$(printf '%s' "$V" | openssl dgst -sha256 -binary | base64 | tr '+/' '-_' | tr -d '=')
    LOC=$(curl -s -o /dev/null -w '%{redirect_url}' \
        "$BASE/oidc/authorize?response_type=code&client_id=$CID&scope=openid&code_challenge=$C&code_challenge_method=S256&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback" \
        -H "Authorization: Bearer $ADMIN")
    echo "$V $(echo "$LOC" | sed -n 's/.*[?&]code=\([^&]*\).*/\1/p')"
}

echo "== N=8 concurrent redemptions of one authorization code =="
read -r V CODE <<<"$(mk_code)"
for i in $(seq 1 8); do
    (curl -s -o /dev/null -w '%{http_code}\n' "$BASE/oidc/token" \
        -H 'Content-Type: application/x-www-form-urlencoded' \
        -d "grant_type=authorization_code&code=$CODE&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback&client_id=$CID&code_verifier=$V") &
done
wait
echo "(expect exactly one 200)"

echo "== N=8 concurrent redemptions of one refresh token =="
read -r V CODE <<<"$(mk_code)"
T0=$(curl -s "$BASE/oidc/token" -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "grant_type=authorization_code&code=$CODE&redirect_uri=http%3A%2F%2Flocalhost%3A9010%2Fauth%2Fcallback&client_id=$CID&code_verifier=$V")
RT=$(echo "$T0" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("refresh_token",""))')
for i in $(seq 1 8); do
    (curl -s -o /dev/null -w '%{http_code}\n' "$BASE/oidc/token" \
        -H 'Content-Type: application/x-www-form-urlencoded' \
        -d "grant_type=refresh_token&refresh_token=$RT&client_id=$CID") &
done
wait
echo "(expect exactly one 200)"
