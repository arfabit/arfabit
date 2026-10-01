#!/bin/sh
# Builds ARFABIT.app, one universal app for Apple silicon and Intel Macs, and
# ARFABIT.dmg holding it beside an Applications shortcut (docs/RELEASE.md §3,
# §5). Run from anywhere, on a Mac with Go and Xcode, whose asset compiler
# makes the icon:
#
#   shell/macos/build.sh            a "dev" build
#   shell/macos/build.sh v0.1.0     a release
#
# Everything goes in dist/. The app is signed with the identity named by
# ARFABIT_SIGN_IDENTITY, the self-made certificate a release is signed with,
# or, when that is not set, ad hoc, which runs on this Mac only.

set -eu

cd "$(dirname "$0")/../.."
ROOT=$(pwd)

VERSION=${1:-dev}
case "$VERSION" in
  dev) SHORT=0.0.0 ;;
  v[0-9]*) SHORT=${VERSION#v} ;;
  *) echo "The version must look like v0.1.0, or be left out for a dev build." >&2; exit 1 ;;
esac

DIST="$ROOT/dist"
WORK="$DIST/macos"
APP="$DIST/ARFABIT.app"
rm -rf "$WORK" "$APP" "$DIST/ARFABIT.dmg"
mkdir -p "$WORK"

echo "ARFABIT $VERSION"

# Work files are named core and shell, not arfabit and ARFABIT: a Mac's disk
# does not tell upper case from lower, so those two would be one file.

# ARFABIT itself, for both kinds of Mac, cgo off, as one file.
for arch in arm64 amd64; do
  CGO_ENABLED=0 GOOS=darwin GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X github.com/arfabit/arfabit/internal/version.Version=$VERSION" \
    -o "$WORK/core-$arch" ./cmd/arfabit
done
lipo -create -output "$WORK/core" "$WORK/core-arm64" "$WORK/core-amd64"

# The shell, for both kinds of Mac, as one file.
for arch in arm64 x86_64; do
  swiftc -O -target "$arch-apple-macos13.0" \
    -module-cache-path "$WORK/module-cache" \
    -o "$WORK/shell-$arch" shell/macos/Sources/*.swift
done
lipo -create -output "$WORK/shell" "$WORK/shell-arm64" "$WORK/shell-x86_64"

# The bundle.
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
sed -e "s/__VERSION__/$VERSION/g" -e "s/__SHORT_VERSION__/$SHORT/g" \
  shell/macos/Info.plist > "$APP/Contents/Info.plist"
cp "$WORK/shell" "$APP/Contents/MacOS/ARFABIT"
cp "$WORK/core" "$APP/Contents/Resources/arfabit"

# The icon, made in Icon Composer. Xcode's asset compiler turns it into
# Assets.car for current macOS and ARFABIT.icns for older versions, and says
# which Info.plist keys name them.
ICON=shell/macos/icon/ARFABIT.icon
if [ -d "$ICON" ]; then
  xcrun actool "$ICON" --compile "$APP/Contents/Resources" \
    --app-icon ARFABIT --platform macosx --target-device mac \
    --minimum-deployment-target 13.0 \
    --output-partial-info-plist "$WORK/icon.plist" \
    --output-format human-readable-text --errors --warnings > "$WORK/actool.txt" 2>&1 \
    || { cat "$WORK/actool.txt" >&2; exit 1; }
  /usr/libexec/PlistBuddy -c "Merge $WORK/icon.plist" "$APP/Contents/Info.plist" > /dev/null
else
  echo "No icon: $ICON is missing, so the app has the generic one."
fi
plutil -lint "$APP/Contents/Info.plist" > /dev/null

# Signing: ARFABIT inside first, then the app around it.
IDENTITY=${ARFABIT_SIGN_IDENTITY:--}
codesign --force --sign "$IDENTITY" --identifier com.arfabit.arfabit.core "$APP/Contents/Resources/arfabit"
codesign --force --sign "$IDENTITY" "$APP"
codesign --verify --strict "$APP"

# The disk image: the app and a shortcut to Applications, so installing is
# one drag.
STAGE="$WORK/dmg"
mkdir -p "$STAGE"
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
hdiutil create -volname ARFABIT -srcfolder "$STAGE" -fs HFS+ -format UDZO -ov "$DIST/ARFABIT.dmg"

echo "Made $APP"
echo "Made $DIST/ARFABIT.dmg"
