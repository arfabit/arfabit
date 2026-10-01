#!/bin/sh
# Makes the self-made certificate ARFABIT.app is signed with, in this Mac's
# login keychain (docs/RELEASE.md §5). Run once, on the Mac releases are
# built on:
#
#   shell/macos/make-certificate.sh
#
# Then build with it:
#
#   ARFABIT_SIGN_IDENTITY="ARFABIT" shell/macos/build.sh v0.1.0
#
# Every release must be signed with this same certificate. macOS remembers
# folder access by it, so a new one would ask for Downloads again after the
# next update. Keep a copy of it (Keychain Access → File → Export Items),
# somewhere other than this Mac.
#
# It does nothing for the first-open step: macOS still asks the person to
# press Open Anyway once.
#
# macOS asks for your password once, to let it sign code.

set -eu

NAME=${1:-ARFABIT}
KEYCHAIN=${KEYCHAIN:-$HOME/Library/Keychains/login.keychain-db}

if security find-certificate -c "$NAME" "$KEYCHAIN" > /dev/null 2>&1; then
  echo "There is already a certificate called \"$NAME\" in $KEYCHAIN. Nothing was changed."
  exit 0
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

cat > "$WORK/certificate.cnf" <<EOF
[req]
distinguished_name = name
x509_extensions = signing
prompt = no

[name]
CN = $NAME

[signing]
basicConstraints = critical, CA:false
keyUsage = critical, digitalSignature
extendedKeyUsage = critical, codeSigning
EOF

# Twenty years: it has to outlast every release signed with it.
/usr/bin/openssl req -x509 -newkey rsa:2048 -nodes -days 7300 -sha256 \
  -config "$WORK/certificate.cnf" \
  -keyout "$WORK/key.pem" -out "$WORK/certificate.pem" 2> /dev/null

# The keychain takes the key and certificate together, as one file with a
# password of its own that is used once, here.
/usr/bin/openssl pkcs12 -export -name "$NAME" \
  -inkey "$WORK/key.pem" -in "$WORK/certificate.pem" \
  -passout pass:import -out "$WORK/identity.p12"

security import "$WORK/identity.p12" -k "$KEYCHAIN" -P import -T /usr/bin/codesign

# Trusted for signing code, by this Mac only.
security add-trusted-cert -r trustRoot -p codeSign -k "$KEYCHAIN" "$WORK/certificate.pem"

echo
echo "Made the certificate \"$NAME\". Build with:"
echo
echo "  ARFABIT_SIGN_IDENTITY=\"$NAME\" shell/macos/build.sh v0.1.0"
