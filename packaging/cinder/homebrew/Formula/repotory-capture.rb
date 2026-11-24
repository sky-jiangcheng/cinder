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
  version "2.45.0"
  license "MIT"

  if OS.mac? && Hardware::CPU.arm?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-darwin-arm64.tar.gz"
    sha256 "09cbf66a16b872e33dedddb0d4d2dee3a9ad44498f44168bcc0389ef2a8bbc00"
  elsif OS.mac? && Hardware::CPU.is_64_bit?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-darwin-amd64.tar.gz"
    sha256 "7f96c92194f320d19605c4bc3b356c27b3c0fc43846c4b8f4e8fb1e59edcbfc3"
  else
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-linux-amd64.tar.gz"
    sha256 "73cd4283dc51eba341aa86a803b7b6a38e1e2c379cb5476cb9c8bb067f80c0ea"
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
