#!/bin/sh
# Makes the GPG key that signs ARFABIT's apt and dnf package repository
# (docs/RELEASE.md §5). apt and dnf refuse a repository that is not signed.
# Run once, on a Mac with GnuPG (brew install gnupg):
#
#   packaging/keys/make-repo-key.sh
#
# It writes:
#   packaging/linux/arfabit.asc          the public half, committed; the
#                                        packages install it so apt and dnf
#                                        trust the repository
#   ~/arfabit-keys/repo-private.asc      the private half, never committed;
#                                        it goes into the GitHub secret
#                                        ARFABIT_REPO_KEY
#
# The key has no passphrase, so the release workflow can use it. It lives only
# in that secret and in the copy you keep somewhere other than this Mac.
#
# Every installed copy trusts this one key, so it is made once and kept.

set -eu

cd "$(dirname "$0")/../.."

PUBLIC=packaging/linux/arfabit.asc
PRIVATE=$HOME/arfabit-keys/repo-private.asc

if ! command -v gpg > /dev/null; then
  echo "GnuPG is needed. Install it with:  brew install gnupg" >&2
  exit 1
fi
for f in "$PUBLIC" "$PRIVATE"; do
  if [ -e "$f" ]; then
    echo "$f already exists, so there is already a repository key. Nothing was changed." >&2
    exit 1
  fi
done

# A keyring of its own, so nothing is added to yours.
GNUPGHOME=$(mktemp -d)
export GNUPGHOME
trap 'rm -rf "$GNUPGHOME"' EXIT

# RSA rather than Ed25519: older rpm on long-supported distributions reads
# only RSA signatures. No expiry: an expired key would stop every update.
gpg --batch --quiet --passphrase '' \
  --quick-generate-key "ARFABIT packages" rsa4096 sign never

mkdir -p "$(dirname "$PRIVATE")"
chmod 700 "$(dirname "$PRIVATE")"
gpg --batch --armor --export "ARFABIT packages" > "$PUBLIC"
( umask 077; gpg --batch --armor --pinentry-mode loopback --passphrase '' \
    --export-secret-keys "ARFABIT packages" > "$PRIVATE" )

echo
echo "Made the repository key."
echo
echo "  Public half, to commit:     $PUBLIC"
echo "  Private half, never commit: $PRIVATE"
echo
echo "Put the private half into GitHub:"
echo
echo "  gh secret set ARFABIT_REPO_KEY --repo arfabit/arfabit < $PRIVATE"
echo
echo "Then keep a copy of it somewhere other than this Mac, such as a password manager."
