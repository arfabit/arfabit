#!/bin/sh
# Makes a release by hand, on the Mac, in two halves either side of the
# Windows build (docs/RELEASE.md §7):
#
#   packaging/release/release.sh start v0.1.0    the Mac app, on a new draft
#   (on Windows: packaging\windows\build.ps1 v0.1.0 -Upload)
#   packaging/release/release.sh finish v0.1.0   latest.json, signed, checked
#
# The tag must exist first, annotated with what changed, and be checked out:
#
#   git tag -a v0.1.0 -m "What changed, in words for the person."
#
# The app is signed with the self-made certificate "ARFABIT"
# (shell/macos/make-certificate.sh), and latest.json with the update key's
# private half in ~/arfabit-keys, which is read here and never printed.

set -eu

cd "$(dirname "$0")/../.."

STEP=${1:-}
VERSION=${2:-}
KEY=${ARFABIT_UPDATE_KEY_FILE:-$HOME/arfabit-keys/update-private.key}

fail() { echo "$*" >&2; exit 1; }

case "$STEP" in
  start|finish) ;;
  *) fail "Say start or finish, then the version: release.sh start v0.1.0" ;;
esac
case "$VERSION" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) fail "The version must look like v0.1.0." ;;
esac

command -v gh > /dev/null || fail "The GitHub CLI is not installed: brew install gh, then gh auth login."

# Both halves build or describe exactly the tagged commit.
[ "$(git cat-file -t "$VERSION" 2> /dev/null)" = tag ] ||
  fail "There is no annotated tag $VERSION. Make it with: git tag -a $VERSION -m \"What changed.\""
[ -z "$(git status --porcelain)" ] || fail "There are changes not committed. Commit or put them away first."
[ "$(git rev-parse HEAD)" = "$(git rev-parse "$VERSION^{commit}")" ] ||
  fail "This checkout is not at $VERSION. Run: git checkout $VERSION"

WORK=dist/release-$VERSION
NOTES=$WORK/notes.txt
mkdir -p "$WORK"
git tag -l --format='%(contents:subject)%0a%0a%(contents:body)' "$VERSION" > "$NOTES"

if [ "$STEP" = start ]; then
  security find-certificate -c ARFABIT > /dev/null 2>&1 ||
    fail "The signing certificate ARFABIT is not in the keychain. Make it once with shell/macos/make-certificate.sh."

  git push origin "$VERSION"
  ARFABIT_SIGN_IDENTITY=ARFABIT shell/macos/build.sh "$VERSION"

  BUILT=$(plutil -extract ARFABITVersion raw dist/ARFABIT.app/Contents/Info.plist)
  [ "$BUILT" = "$VERSION" ] || fail "The app says it is $BUILT, not $VERSION."

  gh release create "$VERSION" --draft --verify-tag \
    --title "ARFABIT $VERSION" --notes-file "$NOTES" \
    dist/ARFABIT.dmg dist/ARFABIT-macOS.zip

  echo
  echo "Made the draft release $VERSION with the Mac app. Next, on Windows:"
  echo
  echo "  git fetch --tags"
  echo "  git checkout $VERSION"
  # printf, not echo: the Mac's sh reads "\b" in an echo as a backspace.
  printf '  packaging\\windows\\build.ps1 %s -Upload\n' "$VERSION"
  echo
  echo "Then here: packaging/release/release.sh finish $VERSION"
  exit 0
fi

# finish: sign what is on the draft, not what is in dist, since that is what
# people download.
[ -f "$KEY" ] || fail "The update key's private half is not at $KEY."

FILES=$WORK/files
rm -rf "$FILES"
mkdir -p "$FILES"
gh release download "$VERSION" -D "$FILES" \
  -p ARFABIT.dmg -p ARFABIT-macOS.zip -p ARFABIT-setup.exe
for f in ARFABIT.dmg ARFABIT-macOS.zip ARFABIT-setup.exe; do
  [ -f "$FILES/$f" ] || fail "$f is not on the draft release $VERSION yet."
done

ARFABIT_UPDATE_KEY=$(cat "$KEY") go run ./packaging/release/latest \
  -version "$VERSION" -notes "$NOTES" \
  -darwin "$FILES/ARFABIT-macOS.zip" -windows "$FILES/ARFABIT-setup.exe" \
  -out "$WORK/latest.json"
gh release upload "$VERSION" "$WORK/latest.json" --clobber

# Read everything back from the release, as a copy of ARFABIT would find it.
CHECK=$WORK/check
rm -rf "$CHECK"
mkdir -p "$CHECK"
gh release download "$VERSION" -D "$CHECK" \
  -p latest.json -p ARFABIT-macOS.zip -p ARFABIT-setup.exe
go run ./packaging/release/latest -version "$VERSION" -check "$CHECK/latest.json"

echo
echo "The draft release $VERSION is complete. Try it, then publish it on GitHub:"
echo
echo "  gh release view $VERSION --web"
echo
echo "Publishing is what sends it to every copy. If a file on it is replaced"
echo "afterwards, run finish again, or every copy will refuse the update."
