# Cinder capture CLI (Linux)
#
# The SessionEnd hook the setup installs calls `repotory-capture` at the end of
# every Claude Code session (ADR-0010 / ADR-0054): it reads the transcript
# directly and writes a handoff note without calling any model. A pure Go /
# zero-CGO binary, so it installs cleanly everywhere.
#
# Version is stamped by scripts/update-manifests.sh from wails.json
# (info.productVersion) — never edit it by hand.
class RepotoryCapture < Formula
  desc "Session-end capture CLI that writes Cinder handoff notes from Claude Code sessions"
  homepage "https://github.com/sky-jiangcheng/cinder"
  version "2.39.0"
  license "MIT"

  if OS.mac? && Hardware::CPU.arm?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-darwin-arm64.tar.gz"
    sha256 "__FILL_SHA256_cinder-capture_darwin_arm64__"
  elsif OS.mac? && Hardware::CPU.is_64_bit?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-darwin-amd64.tar.gz"
    sha256 "__FILL_SHA256_cinder-capture_darwin_amd64__"
  else
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-linux-amd64.tar.gz"
    sha256 "__FILL_SHA256_cinder-capture_linux_amd64__"
  end

  def install
    bin.install "cinder-capture"
    # The hook script calls `command -v repotory-capture`; that name must
    # resolve or the zero-credit primary path never runs.
    bin.install_symlink bin/"cinder-capture" => "repotory-capture"
  end

  # The binary is a SessionEnd hook target: running it outside a session would
  # write a pending-failure record into the user's data dir. Assert the file
  # landed and is executable instead of pretending to run it.
  test do
    assert_path_exists bin/"cinder-capture"
    assert_predicate bin/"cinder-capture", :executable?
  end
end
