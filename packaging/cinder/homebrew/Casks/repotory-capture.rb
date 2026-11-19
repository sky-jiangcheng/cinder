# Cinder capture CLI (macOS)
#
# The SessionEnd hook the setup installs calls `repotory-capture` at the end of
# every Claude Code session (ADR-0010 / ADR-0054): it reads the transcript
# directly and writes a handoff note without calling any model — the zero-credit
# path that keeps working when credits are gone. Without this binary on PATH
# that primary path is dead and every session end falls back to a model call.
#
# Version is stamped by scripts/update-manifests.sh from wails.json
# (info.productVersion) — never edit it by hand.
cask "repotory-capture" do
  version "2.40.2"
  name "Cinder Capture CLI"
  desc "Session-end capture CLI that writes Cinder handoff notes from Claude Code sessions"
  homepage "https://github.com/sky-jiangcheng/cinder"
  # NOTE: no `license` stanza — `license` is a FORMULA method, not a Cask
  # one. `brew install --cask` rejects the whole definition with
  # "undefined method 'license' for Cask". The license is MIT; see README.

  on_arm do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-darwin-arm64.tar.gz"
    sha256 "6e7c5ae17ccf5a27295c7c8b8488c39d6b01d271c58abb3e47b4203373c741e8"
  end

  on_intel do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-darwin-amd64.tar.gz"
    sha256 "b18b7433545bff457edeca4099d9736a0ac554410f24a41ecfa758668c69e148"
  end

  # The tarball stages `cinder-capture` plus a `repotory-capture` symlink;
  # `binary` links both into $(brew --prefix)/bin. The hook script calls
  # `command -v repotory-capture`, so that name has to keep resolving.
  binary "cinder-capture"
  binary "cinder-capture", target: "repotory-capture"
end
