# Cinder MCP server (macOS)
#
# The MCP stdio server is the AI execution interface for Cinder — the desktop
# app is not required to use it, it reads the same local SQLite database.
#
# Version is stamped by scripts/update-manifests.sh from wails.json
# (info.productVersion) — never edit it by hand.
cask "repotory-mcp" do
  version "2.33.0"
  name "Cinder MCP Server"
  desc "MCP stdio server exposing the Cinder local knowledge base to AI agents"
  homepage "https://github.com/sky-jiangcheng/cinder"
  # NOTE: no `license` stanza — `license` is a FORMULA method, not a Cask
  # one. `brew install --cask` rejects the whole definition with
  # "undefined method 'license' for Cask". The license is MIT; see README.

  on_arm do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-arm64.tar.gz"
    sha256 "__FILL_SHA256_CINDER_MCP_TGZ__"
  end

  on_intel do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-amd64.tar.gz"
    sha256 "__FILL_SHA256_CINDER_MCP_TGZ__"
  end

  # The tarball stages `cinder-mcp` plus a `repotory-mcp` symlink; `binary`
  # links both into $(brew --prefix)/bin. The old name is the one every
  # existing client config already points at, so it has to keep resolving.
  binary "cinder-mcp"
  binary "cinder-mcp", target: "repotory-mcp"
end
