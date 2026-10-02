#!/bin/sh
# Stages dist/ARFABIT-macOS.zip as if ARFABIT had downloaded and checked it,
# to try installing an update on this Mac without publishing anything
# (docs/RELEASE.md §12, "Trying an update").
#
#   shell/macos/build.sh v0.2.0 && shell/macos/stage-update.sh
#
# Then choose Restart ARFABIT from the menu bar of an installed, older copy.
set -eu
cd "$(dirname "$0")/../.."

ZIP=dist/ARFABIT-macOS.zip
DIR="$HOME/Library/Application Support/arfabit/update"
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

unzip -p "$ZIP" ARFABIT.app/Contents/Info.plist > "$T/Info.plist"
V=$(plutil -extract ARFABITVersion raw "$T/Info.plist")
NAME="ARFABIT-$V-darwin.zip"

mkdir -p "$DIR"
cp "$ZIP" "$DIR/$NAME"
SUM=$(shasum -a 256 "$DIR/$NAME" | cut -d' ' -f1)
printf '{"version":"%s","notes":"A test of installing a staged update.","file":"%s","sha256":"%s"}\n' \
  "$V" "$NAME" "$SUM" > "$DIR/staged.json"

echo "Staged ARFABIT $V in $DIR."
echo "Now choose Restart ARFABIT from its menu."
