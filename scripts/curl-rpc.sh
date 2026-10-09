#!/usr/bin/env bash
# Wrapper for calling Connect RPC endpoints with cURL.
# Usage: ./curl-rpc.sh <host>/<endpoint> [extra-curl-args...]
# The endpoint is the full procedure path including the /rpc prefix:
#   ./curl-rpc.sh http://localhost:3000/rpc/saka.authn.v1.AuthService/SignIn '{"identity":"admin","password":"secret"}'
#
# The banner goes to stderr, so `./curl-rpc.sh ... | jq` reads the response body alone.

set -euo pipefail

if [ $# -lt 2 ]; then
    echo "Usage: $0 <host>/<endpoint> [extra-curl-args...]" >&2
    echo "" >&2
    echo "The endpoint is the full procedure path, /rpc prefix included:" >&2
    echo "  $0 http://localhost:3000/rpc/saka.authn.v1.AuthService/SignIn '{\"identity\":\"admin\",\"password\":\"secret\"}'" >&2
    echo "  $0 https://api.example.com/rpc/saka.identity.v1.UserService/GetUser '{\"id\":\"123\"}' --verbose" >&2
    exit 1
fi

URL="$1"
DATA="$2"
shift 2

echo "Calling: ${URL}" >&2
echo "Data: ${DATA}" >&2
echo "" >&2

curl \
    --header "Content-Type: application/json" \
    --data "${DATA}" \
    "$@" \
    "${URL}"
