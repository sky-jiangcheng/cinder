# Cinder Desktop app (macOS)
#
# Version is stamped by scripts/update-manifests.sh from wails.json
# (info.productVersion) — never edit it by hand.
#
# sha256 must be filled after each release is published; Homebrew hard-fails on
# a mismatch, which is exactly what we want from a placeholder.
cask "repotory" do
  version "2.32.2"
  name "Cinder Desktop"
  desc "Local-first code project context base"
  homepage "https://github.com/sky-jiangcheng/cinder"
  # NOTE: no `license` stanza — `license` is a FORMULA method, not a Cask
  # one. `brew install --cask` rejects the whole definition with
  # "undefined method 'license' for Cask". The license is MIT; see README.

  on_arm do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/repotory-darwin-arm64.dmg"
    sha256 "bdc7080ad84dbf20fb99dabdc096f005b192cb2ebea7b6f7e35c159d531b8f10"
  end

  on_intel do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/repotory-darwin-amd64.dmg"
    sha256 "8a77669e2f8165ac99395f7badec25f7cace3b4cde5d455b6627f92416b5a75a"
  end

  # The bundle inside the DMG is named after wails.json's `name`
  # ("Cinder"), NOT after the `outputfilename` ("repotory"): the build
  # produces build/bin/Cinder.app and create-dmg ships that bundle as-is.
  # macOS volumes are case-insensitive by default, so an earlier spelling
  # resolved anyway — but a case-sensitive volume would not find it, and the
  # tap's tests.yml asserts the installed path, so the two must agree
  # exactly. The BINARY inside the bundle stays "repotory" (a frozen
  # identifier); only the bundle directory carries the display name.
  app "Cinder.app"

  # Application Support and Logs paths come from internal/platform; they are
  # where GetDbPath() and GetLogPath() point on macOS. A bundle-identifier
  # preference is deliberately not listed here: wails.json does not set one, so
  # listing a guess would send `brew uninstall --zap` at a file that may not
  # exist. Add it only once the identifier is pinned in wails.json.
  zap trash: [
    "~/Library/Application Support/repotory",
    "~/Library/Logs/repotory.log",
  ]
end
