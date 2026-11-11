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
  version "2.35.5"
  license "MIT"

  if OS.mac? && Hardware::CPU.arm?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-arm64.tar.gz"
    sha256 "2e5cbd62f5d379086a0186431a2a1dac6a1120e6af0e053f052f8f6adcfd6718"
  elsif OS.mac? && Hardware::CPU.is_64_bit?
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-amd64.tar.gz"
    sha256 "b1599fed01bde9590854d46c6756b721df5a9d8bb5c52f7189a36dc2b6779937"
  else
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-linux-amd64.tar.gz"
    sha256 "928e6e9a89d67bb3247f5ec6c0d8be078637b498af0cbd19021a4ed9ed21d7e6"
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
