#!/bin/bash
# Sign and notarise a macOS .app bundle, then wrap it in a notarised .dmg.
#
# This is a chain, and every link has to hold:
#
#   1. import the certificate into a temporary keychain
#   2. make the private key usable without a GUI prompt (set-key-partition-list)
#   3. sign the executable inside the bundle, then the bundle itself
#   4. notarise the app (the .dmg cannot be notarised before the app inside
#      it has a ticket of its own)
#   5. staple the ticket to the app
#   6. build the .dmg around the notarised app
#   7. notarise and staple the .dmg as well
#
# Every step fails the script. That is the whole point: a release that skips
# notarisation still installs, so a silent failure here ships an app that
# pops "Apple cannot verify" for every user, and nobody notices until the
# issue tracker fills up.
#
# Environment:
#   APPLE_CERTIFICATE          base64 of the .p12 (Developer ID Application)
#   APPLE_CERTIFICATE_PASSWORD password used when the .p12 was exported
#   APPLE_SIGNING_IDENTITY     certificate name, e.g.
#                              "Developer ID Application: Name (TEAMID)"
#   APPLE_ID                   Apple ID for notarisation (optional)
#   APPLE_PASSWORD             app-specific password for notarisation (optional)
#   APPLE_TEAM_ID              team id for notarisation (optional)
#   APPLE_API_KEY              App Store Connect key id (optional, preferred)
#   APPLE_API_ISSUER           App Store Connect issuer id (optional)
#   APPLE_API_KEY_PATH         path to the .p8 key file (optional)
#
# The app path is $1 and the .dmg output path is $2.
set -euo pipefail

APP_PATH="${1:?usage: sign-macos.sh <app-path> <dmg-path>}"
DMG_PATH="${2:?usage: sign-macos.sh <app-path> <dmg-path>}"

if [ -z "${APPLE_CERTIFICATE:-}" ]; then
  echo "::error::APPLE_CERTIFICATE is empty — refusing to ship an unsigned build silently."
  echo "See docs/signing.md for how to configure the secrets."
  exit 1
fi
if [ -z "${APPLE_CERTIFICATE_PASSWORD:-}" ]; then
  echo "::error::APPLE_CERTIFICATE_PASSWORD is empty."
  exit 1
fi
if [ -z "${APPLE_SIGNING_IDENTITY:-}" ]; then
  echo "::error::APPLE_SIGNING_IDENTITY is empty — the certificate name is needed to sign."
  exit 1
fi

KEYCHAIN="cinder-signing.keychain"
KEYCHAIN_PASSWORD="$(head -c 32 /dev/urandom | base64 | tr -d '\n')"
CERT_FILE="$(mktemp -t cinder-cert).p12"

# The keychain search list is global state, and the script replaces it so that
# codesign finds the imported identity. It has to be put back: leaving a
# runner's search list pointing at a keychain that is about to be deleted
# breaks every later `security` call on that machine — and on a developer
# laptop it breaks the next unrelated command too.
ORIGINAL_KEYCHAINS="$(security list-keychains -d user | tr -d '\"')"

cleanup() {
  security list-keychains -d user -s $ORIGINAL_KEYCHAINS >/dev/null 2>&1 || true
  security delete-keychain "$KEYCHAIN" >/dev/null 2>&1 || true
  rm -f "$CERT_FILE"
}
trap cleanup EXIT

# ① the certificate, into a keychain of our own so the runner's login keychain
# stays untouched.
#
# `base64 --decode` rather than `openssl base64 -d`: the OpenSSL 3 decoder
# silently produces a ZERO-BYTE file for a long single-line payload (the
# certificate is about 4 KB of base64 with the newlines stripped), and it
# does so with exit code 0. The empty file then reaches `security import`
# as "Unable to decode the provided data", which reads like a corrupt
# certificate and sends you hunting for a password problem that is not
# there. BSD base64 gets it right, and `--decode` is also what GNU
# coreutils spells it, so the same line works on a Linux runner.
echo "$APPLE_CERTIFICATE" | base64 --decode > "$CERT_FILE"
if [ ! -s "$CERT_FILE" ]; then
  echo "::error::the certificate decoded to an empty file."
  echo "::error::APPLE_CERTIFICATE is $(printf '%s' "$APPLE_CERTIFICATE" | wc -c | tr -d ' ') base64 characters."
  exit 1
fi
security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
security set-keychain-settings -lut 21600 "$KEYCHAIN"
security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
# `-T /usr/bin/codesign` plus an explicit partition list: without the latter,
# codesign asks for permission to use the key and the CI runner has nobody to
# ask. This one line is the difference between "signed" and "hung".
#
# The `-f pkcs12` is not optional. OpenSSL 3 writes .p12 files with PBES2/AES
# by default, and macOS `security` cannot read those — it fails with "MAC
# verification failed during PKCS12 import (wrong password?)", which blames the
# password for an encryption-algorithm problem. Naming the format explicitly
# makes `security` fall back to its own reader. (tauri-apps/tauri#15843 is
# the same bug, hit from the other side.)
if ! security import "$CERT_FILE" -k "$KEYCHAIN" -P "$APPLE_CERTIFICATE_PASSWORD" \
     -f pkcs12 -T /usr/bin/codesign 2>/tmp/cinder-import.err; then
  cat /tmp/cinder-import.err
  echo "::error::could not import the signing certificate."
  echo "::error::If the password is right, the .p12 is probably encrypted with a"
  echo "::error::scheme macOS cannot read. Re-export it with the old algorithms:"
  echo "::error::  openssl pkcs12 -in cert.p12 -nodes -out cert.pem -passin pass:YOUR_PASSWORD"
  echo "::error::  openssl pkcs12 -export -inkey cert.pem -in cert.pem -out cert-legacy.p12 \\"
  echo "::error::    -passout pass:YOUR_PASSWORD -keypbe PBE-SHA1-3DES \\"
  echo "::error::    -certpbe PBE-SHA1-3DES -macalg sha1"
  exit 1
fi
security set-key-partition-list -S apple-tool:,apple:,codesign: \
  -s -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
security list-keychains -s "$KEYCHAIN"

# ② the identity must exist, and it must be the one we were told to use.
IDENTITIES="$(security find-identity -v -p codesigning)"
echo "$IDENTITIES"
if ! echo "$IDENTITIES" | grep -q "$APPLE_SIGNING_IDENTITY"; then
  echo "::error::the imported certificate does not provide identity '$APPLE_SIGNING_IDENTITY'."
  echo "The identities available are the ones listed above."
  exit 1
fi

# ③ sign the app, from the inside out.
#
# The executable is signed first and the bundle second, which is the order
# Apple documents. `codesign --deep` would do it in one call, but Apple says
# not to use it for distribution: it re-signs nested code in an order that is
# not always the one the bundle needs.
#
# `--options runtime` (the hardened runtime) and `--timestamp` are both
# required for notarisation: without the hardened runtime Apple refuses the
# upload, and without a secure timestamp the signature expires with the
# certificate rather than surviving it.
echo "::group::Signing $APP_PATH"
if [ -d "$APP_PATH/Contents/MacOS" ]; then
  for binary in "$APP_PATH"/Contents/MacOS/*; do
    [ -f "$binary" ] || continue
    if ! file "$binary" | grep -q "Mach-O"; then
      continue
    fi
    codesign --force --options runtime --timestamp \
      --sign "$APPLE_SIGNING_IDENTITY" \
      --keychain "$KEYCHAIN" \
      "$binary"
  done
fi
codesign --force --options runtime --timestamp \
  --sign "$APPLE_SIGNING_IDENTITY" \
  --keychain "$KEYCHAIN" \
  "$APP_PATH"
codesign --verify --deep --strict --verbose=2 "$APP_PATH"
echo "::endgroup::"

# ④ notarise the app.
#
# Apple notarises a .zip (or a .dmg / .pkg), not a .app, so the app is zipped
# first. The ticket that comes back is then stapled to the app itself, so the
# app carries its own proof of notarisation rather than depending on the
# .dmg's ticket.
echo "::group::Notarising the app"
NOTARY_ZIP="$(mktemp -t cinder-notary).zip"
ditto -c -k --keepParent "$APP_PATH" "$NOTARY_ZIP"

notary_args=()
if [ -n "${APPLE_API_KEY:-}" ] && [ -n "${APPLE_API_ISSUER:-}" ] && [ -n "${APPLE_API_KEY_PATH:-}" ]; then
  # App Store Connect API key: no Apple ID password in the logs, and no
  # two-factor prompt in the middle of a release.
  notary_args=(--key "$APPLE_API_KEY_PATH" --key-id "$APPLE_API_KEY" --issuer "$APPLE_API_ISSUER")
elif [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_PASSWORD:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ]; then
  notary_args=(--apple-id "$APPLE_ID" --password "$APPLE_PASSWORD" --team-id "$APPLE_TEAM_ID")
else
  echo "::error::no notarisation credentials: set either APPLE_API_KEY + APPLE_API_ISSUER +"
  echo "::error::APPLE_API_KEY_PATH, or APPLE_ID + APPLE_PASSWORD + APPLE_TEAM_ID."
  exit 1
fi

xcrun notarytool submit "$NOTARY_ZIP" "${notary_args[@]}" --wait
rm -f "$NOTARY_ZIP"
xcrun stapler staple "$APP_PATH"
xcrun stapler validate "$APP_PATH"
echo "::endgroup::"

# ⑤ the .dmg is built around the notarised app. A notarised app inside an
# un-notarised .dmg still gets the "cannot be verified" dialog, because the
# .dmg is what the user's browser downloads and what Gatekeeper checks first.
# The image is signed (step ⑥) and notarised (step ⑦) in its own right.
echo "::group::Building and notarising the .dmg"
STAGING="$(mktemp -d)"
mkdir -p "$STAGING/.background"
sips -s format png "build/dmg-background.svg" --out "$STAGING/.background/background.png" >/dev/null
cp -R "$APP_PATH" "$STAGING/"
ln -s /Applications "$STAGING/Applications"
APP_NAME="$(basename "$APP_PATH")"

create-dmg \
  --volname "Cinder Desktop" \
  --window-size 600 400 \
  --icon-size 80 \
  --icon "$APP_NAME" 100 280 \
  --hide-extension "$APP_NAME" \
  --app-drop-link 400 280 \
  --background "$STAGING/.background/background.png" \
  --format UDZO \
  "$DMG_PATH" \
  "$STAGING/"
rm -rf "$STAGING"

if [ ! -f "$DMG_PATH" ]; then
  echo "::error::$DMG_PATH was not produced."
  exit 1
fi

# ⑥ the disk image is signed in its own right, and that signature is what
# Gatekeeper reads.
#
# This is the step that is easy to miss. notarytool will happily accept an
# UNSIGNED .dmg, staple a ticket onto it and report "Accepted" — but the
# stapled ticket proves that "something inside this image was notarised", not
# that the image itself is trustworthy. `spctl --assess --type open` then
# answers:
#
#     release/Cinder-x.y.z-darwin-arm64.dmg: rejected
#     source=no usable signature
#
# Apple documents the order as sign → notarise → staple, and the signature
# has to be in place before the submission, because the ticket that comes
# back is bound to the signed bytes.
#
# The .app inside was signed and notarised in step ③/④, so signing the image
# here does not re-sign anything inside it: a disk image is signed as a
# container, not as a bundle.
echo "::group::Signing the .dmg"
codesign --force --timestamp \
  --sign "$APPLE_SIGNING_IDENTITY" \
  --keychain "$KEYCHAIN" \
  "$DMG_PATH"
codesign --verify --verbose=2 "$DMG_PATH"
echo "::endgroup::"

# ⑦ a .dmg is one of the three formats notarytool accepts directly, so it is
# submitted as it is — zipping it first would only add a step.
echo "::group::Notarising the .dmg"
xcrun notarytool submit "$DMG_PATH" "${notary_args[@]}" --wait
xcrun stapler staple "$DMG_PATH"
xcrun stapler validate "$DMG_PATH"
echo "::endgroup::"

# ⑧ the proof. `spctl` is what Gatekeeper itself asks, and a pass here means
# the app opens without any dialog on a machine that has never seen it.
echo "::group::Verifying with Gatekeeper"
spctl --assess --type execute --verbose=4 "$APP_PATH"
spctl --assess --type open --context context:primary-signature --verbose=4 "$DMG_PATH"
echo "::endgroup::"

echo "Signed and notarised: $APP_PATH -> $DMG_PATH"
