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
  version "2.32.4"
  license "MIT"

  if OS.mac? && Hardware::CPU.arm?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/repotory-mcp-darwin-arm64.tar.gz"
    sha256 "30ed31b9456b8f214bac835d6fe6584b76f02a1ca75b15d4c23be73fc3b94fd4"
  elsif OS.mac? && Hardware::CPU.is_64_bit?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/repotory-mcp-darwin-amd64.tar.gz"
    sha256 "fd4b72624770d32f9d4a3cd3747d264ff7e045e3df45382ca32d64e9212d96bc"
  else
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/repotory-mcp-linux-amd64.tar.gz"
    sha256 "089f9672cdc8fad6de3bc8af198461667bc5364942eafeee7550ac413caf5d1c"
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
