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
  version "2.40.2"
  license "MIT"

  if OS.mac? && Hardware::CPU.arm?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-darwin-arm64.tar.gz"
    sha256 "6e7c5ae17ccf5a27295c7c8b8488c39d6b01d271c58abb3e47b4203373c741e8"
  elsif OS.mac? && Hardware::CPU.is_64_bit?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-darwin-amd64.tar.gz"
    sha256 "b18b7433545bff457edeca4099d9736a0ac554410f24a41ecfa758668c69e148"
  else
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-capture-#{version}-linux-amd64.tar.gz"
    sha256 "8bb7d462a593f2337c3e9fe1919c3eb5bbd479c018e2099a4f39dec98386481e"
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
