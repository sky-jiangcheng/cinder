# Cinder MCP server (Linux)
#
# The desktop app has no Linux installer artifact (it ships as a bare tarball),
# but the MCP server is a pure Go / zero-CGO binary and installs cleanly, so the
# Formula covers MCP on Linux rather than the GUI app.
#
# Version is stamped by scripts/update-manifests.sh from wails.json
# (info.productVersion) — never edit it by hand.
class RepotoryMcp < Formula
  desc "MCP stdio server exposing the Cinder local knowledge base to AI agents"
  homepage "https://github.com/sky-jiangcheng/cinder"
  version "2.33.1"
  license "MIT"

  if OS.mac? && Hardware::CPU.arm?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-arm64.tar.gz"
    sha256 "d48a10864bc1bdfc9b841fbce5714271e0c34ddc4f8a2282db5d4547d8c39ed3"
  elsif OS.mac? && Hardware::CPU.is_64_bit?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-amd64.tar.gz"
    sha256 "2bf41177f834f279dae0ed55a56bf26f16b7334d7c943b755ee880abf4d5b313"
  else
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-linux-amd64.tar.gz"
    sha256 "18ace8ab7db2bb6fdbc8586f383aba2718b0461df938b9bdb81fe36d1402ca44"
  end

  def install
    bin.install "cinder-mcp"
    # Pre-rename name, for the client configs that already point at it.
    bin.install_symlink bin/"cinder-mcp" => "repotory-mcp"
  end

  # The binary is a stdio MCP server: running it with no stdin makes it block
  # on the JSON-RPC loop, so it cannot be executed as a test. Assert the file
  # landed and is executable instead of pretending to run it.
  test do
    assert_path_exists bin/"cinder-mcp"
    assert_predicate bin/"cinder-mcp", :executable?
  end
end
