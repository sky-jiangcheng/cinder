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
  version "2.33.4"
  license "MIT"

  if OS.mac? && Hardware::CPU.arm?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-arm64.tar.gz"
    sha256 "1d0a43a1f7f9d175119b9be0b859572ea283d0c5c1b7897bcf2df27b4ee33b14"
  elsif OS.mac? && Hardware::CPU.is_64_bit?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-amd64.tar.gz"
    sha256 "e4c3af576995ed1bc66e7e029d9001dea8f25e03c1654397cdc829bb687174dd"
  else
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-linux-amd64.tar.gz"
    sha256 "829c789ce179c61d0120e9c8bc4d88f6aedbb03a62d016fa36bc98e970ebc680"
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
