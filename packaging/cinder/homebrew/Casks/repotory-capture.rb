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
  version "2.43.0"
  name "Cinder Capture CLI"
  desc "Session-end capture CLI that writes Cinder handoff notes from Claude Code sessions"
  homepage "https://github.com/sky-jiangcheng/cinder"
  # NOTE: no `license` stanza — `license` is a FORMULA method, not a Cask
  # one. `brew install --cask` rejects the whole definition with
  # "undefined method 'license' for Cask". The license is MIT; see README.

  on_arm do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-darwin-arm64.tar.gz"
    sha256 "31464b172041e1a0335006e891f73d028cbc545557fe3bb3a02f751c7d0825b6"
  end

  on_intel do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-darwin-amd64.tar.gz"
    sha256 "d15474ad458776ec05b1f439aa1b702de9c92e9e71936f854b277ba41f6abf20"
  end

  # The tarball stages `cinder-capture` plus a `repotory-capture` symlink;
  # `binary` links both into $(brew --prefix)/bin. The hook script calls
  # `command -v repotory-capture`, so that name has to keep resolving.
  binary "cinder-capture"
  binary "cinder-capture", target: "repotory-capture"
end
