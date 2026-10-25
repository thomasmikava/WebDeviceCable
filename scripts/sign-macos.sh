#!/bin/sh
# Operator-run release step. No credentials are stored in this repository.
set -eu
: "${WDC_SIGN_IDENTITY:?Set the Developer ID Application identity}"
bridge_app="${1:?Usage: sign-macos.sh /path/to/WebDeviceCable.app}"
codesign --force --options runtime --timestamp --sign "$WDC_SIGN_IDENTITY" "$bridge_app/Contents/Resources/webdevicecable"
codesign --force --options runtime --timestamp --sign "$WDC_SIGN_IDENTITY" "$bridge_app"
codesign --verify --deep --strict --verbose=2 "$bridge_app"
if [ -n "${WDC_NOTARY_PROFILE:-}" ]; then
  bridge_archive="${bridge_app%/}.notary.zip"
  ditto -c -k --keepParent "$bridge_app" "$bridge_archive"
  xcrun notarytool submit "$bridge_archive" --keychain-profile "$WDC_NOTARY_PROFILE" --wait
  xcrun stapler staple "$bridge_app"
  xcrun stapler validate "$bridge_app"
  rm -f "$bridge_archive"
fi
