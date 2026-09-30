#!/usr/bin/env bash

# 1. Detect and check the operating system
OS_TYPE="$(uname -s)"
CURL_OPTS=("-sfL")

case "$OS_TYPE" in
Linux*)
    echo "[INFO] System: Linux environment verified"
    ;;
Darwin*)
    echo "[INFO] System: macOS (Darwin) environment verified"
    ;;
*)
    echo "[ERROR] Unsupported Operating System ($OS_TYPE)"
    echo "[INFO] This script is strictly designed for Linux and macOS environments."
    exit 1
    ;;
esac

# 2. Check if curl is available before proceeding
if ! command -v curl &>/dev/null; then
    echo "[ERROR] The 'curl' dependency is missing"
    echo "[INFO] Please install curl first (e.g., 'sudo apt install curl' or 'brew install curl')."
    exit 1
fi

# 3. Determine project directory paths dynamically based on script location
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
OUTPUT_FILE="$PROJECT_DIR/modules/identity/webauthn/aaguid.json"
TEMP_FILE="$PROJECT_DIR/modules/identity/webauthn/aaguid.tmp.json"

# The raw endpoint for the combined data stream
URL="https://raw.githubusercontent.com/passkeydeveloper/passkey-authenticator-aaguids/main/combined_aaguid.json"

echo "[INFO] Fetching the latest WebAuthn AAGUID definitions..."

# 4. Ensure the output directory exists
mkdir -p "$(dirname "$OUTPUT_FILE")"

# 5. Download the new data into a temporary file using optimized flags
if ! curl "${CURL_OPTS[@]}" -o "$TEMP_FILE" "$URL"; then
    echo "[ERROR] Network data synchronization failed"
    echo "[INFO] Troubleshoot:"
    echo "       - Check your local internet connectivity."
    echo "       - Verify that the GitHub raw asset endpoint is reachable."
    rm -f "$TEMP_FILE"
    exit 1
fi

# 6. Validate and trim. The upstream combined file carries the icons inline
# as base64 data URLs — megabytes the binary does not need. The embedded
# manifest keeps the names; the icons are a later surface with their own
# content-addressed pipeline (TODO(passkey)).
if command -v jq &>/dev/null; then
    if ! jq -e '. | objects and (keys | length > 0)' "$TEMP_FILE" >/dev/null 2>&1; then
        echo "[WARN] Data validation failed (Malformed JSON or empty array received)"
        echo "[INFO] System fallback triggered: Retaining previous data to avoid system disruption."
        rm -f "$TEMP_FILE"
        exit 1
    fi
    TRIMMED_FILE="$PROJECT_DIR/modules/identity/webauthn/aaguid.trimmed.json"
    jq 'map_values({name: .name})' "$TEMP_FILE" > "$TRIMMED_FILE" \
        && mv "$TRIMMED_FILE" "$TEMP_FILE"
else
    echo "[ERROR] 'jq' is required to trim the AAGUID catalog"
    echo "[INFO] The upstream payload carries inline icons the embed must not carry."
    rm -f "$TEMP_FILE"
    exit 1
fi

# 7. Swap the temp file with the output file if all checks pass
mv "$TEMP_FILE" "$OUTPUT_FILE"
FILE_SIZE=$(ls -lh "$OUTPUT_FILE" | awk '{print $5}')
RELATIVE_PATH="${OUTPUT_FILE#$PROJECT_DIR/}"
echo "[SUCCESS] AAGUID payload refreshed successfully"
echo "[INFO] Path: $RELATIVE_PATH"
echo "[INFO] Size: $FILE_SIZE"
