# Cinder MCP server (macOS)
#
# The MCP stdio server is the AI execution interface for Cinder — the desktop
# app is not required to use it, it reads the same local SQLite database.
#
# Version is stamped by scripts/update-manifests.sh from wails.json
# (info.productVersion) — never edit it by hand.
cask "repotory-mcp" do
  version "2.39.2"
  name "Cinder MCP Server"
  desc "MCP stdio server exposing the Cinder local knowledge base to AI agents"
  homepage "https://github.com/sky-jiangcheng/cinder"
  # NOTE: no `license` stanza — `license` is a FORMULA method, not a Cask
  # one. `brew install --cask` rejects the whole definition with
  # "undefined method 'license' for Cask". The license is MIT; see README.

  on_arm do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-arm64.tar.gz"
    sha256 "bd433bbc29e299ba1baba9c1ffa858bcbb4e235eb6413c22f53601d7fcce3f8e"
  end

  on_intel do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-amd64.tar.gz"
    sha256 "2de39d1e112d5972a08b2f23f875cc7fbd9e950f5aca2bb67b6b25d1ff4413e3"
  end

  # The tarball stages `cinder-mcp` plus a `repotory-mcp` symlink; `binary`
  # links both into $(brew --prefix)/bin. The old name is the one every
  # existing client config already points at, so it has to keep resolving.
  binary "cinder-mcp"
  binary "cinder-mcp", target: "repotory-mcp"
end
