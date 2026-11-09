# Cinder Desktop app (macOS)
#
# Version is stamped by scripts/update-manifests.sh from wails.json
# (info.productVersion) — never edit it by hand.
#
# sha256 must be filled after each release is published; Homebrew hard-fails on
# a mismatch, which is exactly what we want from a placeholder.
cask "repotory" do
  version "2.33.3"
  name "Cinder Desktop"
  desc "Local-first code project context base"
  homepage "https://github.com/sky-jiangcheng/cinder"
  # NOTE: no `license` stanza — `license` is a FORMULA method, not a Cask
  # one. `brew install --cask` rejects the whole definition with
  # "undefined method 'license' for Cask". The license is MIT; see README.

  on_arm do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/Cinder-#{version}-darwin-arm64.dmg"
    sha256 "96c6d5f49ac82e31a4a8d23b499e6fa4cfd362f2b01e36146071750347de599b"
  end

  on_intel do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/Cinder-#{version}-darwin-amd64.dmg"
    sha256 "c38d1bf0df786e1c1a23ff94f698e772abb429b108cee1dd21ae5be11782d35e"
  end

  # The bundle inside the DMG is named after wails.json's `name`
  # ("Cinder"), NOT after the `outputfilename` ("cinder", the executable
  # inside the bundle): the build produces build/bin/Cinder.app and
  # create-dmg ships that bundle as-is.
  # macOS volumes are case-insensitive by default, so an earlier spelling
  # resolved anyway — but a case-sensitive volume would not find it, and the
  # tap's tests.yml asserts the installed path, so the two must agree
  # exactly. The BINARY inside the bundle is `cinder` (wails.json
  # outputfilename); this stanza only tracks the bundle directory.
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
