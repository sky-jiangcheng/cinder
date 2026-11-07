# Cinder Desktop app (macOS)
#
# Version is stamped by scripts/update-manifests.sh from wails.json
# (info.productVersion) — never edit it by hand.
#
# sha256 must be filled after each release is published; Homebrew hard-fails on
# a mismatch, which is exactly what we want from a placeholder.
cask "repotory" do
  version "2.32.5"
  name "Cinder Desktop"
  desc "Local-first code project context base"
  homepage "https://github.com/sky-jiangcheng/cinder"
  # NOTE: no `license` stanza — `license` is a FORMULA method, not a Cask
  # one. `brew install --cask` rejects the whole definition with
  # "undefined method 'license' for Cask". The license is MIT; see README.

  on_arm do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/repotory-darwin-arm64.dmg"
    sha256 "59e7f3651e5cf1fd3eab8edcf1cfc83c3a01249f0253459f17fce1384b0084ee"
  end

  on_intel do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/repotory-darwin-amd64.dmg"
    sha256 "0ed8dac81c7781b15f1b55e585e62d0392a8917ae6c05e15db5d76315e4784c5"
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
