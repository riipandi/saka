#!/usr/bin/env bash
# Drives the tus endpoint end to end: creation with the first chunk, a HEAD
# offset probe, the final PATCH that reaches the declared length, and the
# /storage read that names the same bytes back.
set -euo pipefail

BASE="${BASE:-http://localhost:3080}"
TOKEN="$1"
BUCKET="${2:-default}"
KEY="${3:-probe/tus-e2e.txt}"

body() {
    printf 'tus-e2e-first-chunk-'
    head -c 2000 /dev/zero | tr '\0' 'a'
}

CHUNK1="first-half-"
CHUNK2="second-half!"
LENGTH=$((${#CHUNK1} + ${#CHUNK2}))

meta_b64() { printf '%s' "$1" | base64; }

echo "== OPTIONS =="
curl -s -i -X OPTIONS "$BASE/api/uploads" | grep -iE "^(HTTP|tus-)" | head -4

echo "== POST (creation with upload) =="
CREATE=$(curl -s -i -X POST "$BASE/api/uploads" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Upload-Length: $LENGTH" \
    -H "Upload-Metadata: bucket $(meta_b64 "$BUCKET"), key $(meta_b64 "$KEY"), filetype $(meta_b64 "text/plain")" \
    --data-binary "$CHUNK1")
echo "$CREATE" | grep -iE "^(HTTP|location|upload-offset|tus-resumable)"

echo "== HEAD (resume probe) =="
HEAD=$(curl -s -I "$BASE/api/uploads/$BUCKET/$KEY" -H "Authorization: Bearer $TOKEN")
echo "$HEAD" | grep -i "upload-offset"
OFFSET=$(echo "$HEAD" | grep -i "^upload-offset:" | tr -dc '0-9')

echo "== PATCH (final chunk) =="
curl -s -i -X PATCH "$BASE/api/uploads/$BUCKET/$KEY" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/offset+octet-stream" \
    -H "Upload-Offset: $OFFSET" \
    --data-binary "$CHUNK2" | grep -iE "^(HTTP|upload-offset)"

echo "== /storage read =="
sleep 3
curl -s "$BASE/storage/$BUCKET/$KEY" | tail -c "${#CHUNK2}"
echo

echo "== DELETE (termination) =="
curl -s -i -X DELETE "$BASE/api/uploads/$BUCKET/$KEY" -H "Authorization: Bearer $TOKEN" | grep "^HTTP"
