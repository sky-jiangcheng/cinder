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
  version "2.32.3"
  license "MIT"

  if OS.mac? && Hardware::CPU.arm?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/repotory-mcp-darwin-arm64.tar.gz"
    sha256 "3355a40b7a9abcda8ae66f03455153d7602cad9a83c1aabe00b1a801f235b34a"
  elsif OS.mac? && Hardware::CPU.is_64_bit?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/repotory-mcp-darwin-amd64.tar.gz"
    sha256 "e014dd4a132385cad8c44917ae34b160f83e831000fb99457cf2c6c995a7fe89"
  else
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/repotory-mcp-linux-amd64.tar.gz"
    sha256 "d7af367283fde0292de148d202feaddacc1134e8ff3df449890f1f444728baad"
  end

  def install
    bin.install "repotory-mcp"
  end

  # The binary is a stdio MCP server: running it with no stdin makes it block
  # on the JSON-RPC loop, so it cannot be executed as a test. Assert the file
  # landed and is executable instead of pretending to run it.
  test do
    assert_path_exists bin/"repotory-mcp"
    assert_predicate bin/"repotory-mcp", :executable?
  end
end
