#!/usr/bin/env bash
# The PKCS12 truststore the conformance suite's JVM imports: the suite keeps
# its own trust store, so the local CA rides in as a bundle the compose
# service mounts and JAVA_TOOL_OPTIONS names. mkcert's root CA is the anchor
# when it exists; the openssl self-signed certificate is its own anchor
# otherwise. The bundle is certs only (-nokeys): nothing private leaves the
# host.
set -euo pipefail

CERT_DIR=storage/config
mkdir -p "$CERT_DIR"

MKCERT_ROOT="$(mkcert -CAROOT 2>/dev/null)/rootCA.pem"
if [ -f "$MKCERT_ROOT" ]; then
    CA="$MKCERT_ROOT"
elif [ -f "$HOME/Library/Application Support/mkcert/rootCA.pem" ]; then
    CA="$HOME/Library/Application Support/mkcert/rootCA.pem"
else
    CA="$CERT_DIR/localhost_crt.pem"
fi

echo "trust anchor: $CA"
openssl pkcs12 -export -nokeys -in "$CA" \
    -out "$CERT_DIR/conformance-trust.p12" -passout pass:changeit
echo "truststore: $CERT_DIR/conformance-trust.p12"
