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
    sha256 "c0179e0c57d7118f0effce2b52d29d627c5391dfca739a9eebaf7730a6de4765"
  elsif OS.mac? && Hardware::CPU.is_64_bit?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/repotory-mcp-darwin-amd64.tar.gz"
    sha256 "a06b02f6ff98f9c5579e23293e9301fd102c4a2c31ecae73289779febfccbcaa"
  else
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/repotory-mcp-linux-amd64.tar.gz"
    sha256 "f5abc2beb631f568430f7111151460bb15e2183d17faa74c1388b2cb7ebfeb55"
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
