#!/usr/bin/env bash
# Reject credential literals in the tracked Yaak export.
#
# The workspace is exported to api/specs/ and committed, so a value typed into a
# request body lands in git. Commit a23ddcd removed the credentials once; commit
# 4a095a6 reformatted 131 request bodies and wrote two back. This check is the
# guard against a third time.
#
# A credential field is accepted when its value is empty, a REPLACE_* placeholder,
# or a ${[ ... ]} template reference. Anything else is a literal.
#
# Usage: check-yaak-secrets.sh <file> [file...]
set -euo pipefail

status=0

report() {
    echo "$1: $2" >&2
    echo "  $3" >&2
    status=1
}

for file in "$@"; do
    [ -f "$file" ] || continue

    # Credential-shaped JSON fields.
    while IFS= read -r match; do
        [ -n "$match" ] || continue
        value=$(printf '%s' "$match" | sed -E 's/^"[a-z_]+"[[:space:]]*:[[:space:]]*"(.*)"$/\1/')
        [ -n "$value" ] || continue
        case "$value" in
        REPLACE_*) continue ;;
        *'${['*) continue ;;
        esac
        report "$file" "credential field holds a literal" "$match"
    done < <(grep -oE '"(secret|password|current_password|new_password)"[[:space:]]*:[[:space:]]*"[^"]*"' "$file" || true)

    # A JWT literal in a header or body.
    while IFS= read -r match; do
        [ -n "$match" ] || continue
        report "$file" "bearer JWT literal; use \${[ accessToken ]}" "$match"
    done < <(grep -oE 'Bearer eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+' "$file" || true)

    # An API key literal outside a placeholder.
    while IFS= read -r match; do
        [ -n "$match" ] || continue
        report "$file" "API key literal; use \${[ apiKey ]}" "$match"
    done < <(grep -oE '\b(pik|sk)_[A-Za-z0-9]{12,}' "$file" | grep -v 'REPLACE_' || true)
done

if [ "$status" -ne 0 ]; then
    echo "" >&2
    echo "Credentials belong in a Yaak environment, not in the tracked export." >&2
fi

exit "$status"
