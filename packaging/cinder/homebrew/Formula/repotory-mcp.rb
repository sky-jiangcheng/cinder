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
  version "2.35.2"
  license "MIT"

  if OS.mac? && Hardware::CPU.arm?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-arm64.tar.gz"
    sha256 "6c5bd7f90092a66100295dc0ac34e12571c1a41c57636fafcca04ce8e7e27cb0"
  elsif OS.mac? && Hardware::CPU.is_64_bit?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-amd64.tar.gz"
    sha256 "c726b39519c52e29a63bf7f1b91e64332c775fe62703997a719f78b79e419972"
  else
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-linux-amd64.tar.gz"
    sha256 "575fb42f990cc0be678d886d64c353b1d13650cd0527ffd496646186b1f3d7a2"
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
