# Cinder MCP server (macOS)
#
# The MCP stdio server is the AI execution interface for Cinder — the desktop
# app is not required to use it, it reads the same local SQLite database.
#
# Version is stamped by scripts/update-manifests.sh from wails.json
# (info.productVersion) — never edit it by hand.
cask "repotory-mcp" do
  version "2.45.1"
  name "Cinder MCP Server"
  desc "MCP stdio server exposing the Cinder local knowledge base to AI agents"
  homepage "https://github.com/sky-jiangcheng/cinder"
  # NOTE: no `license` stanza — `license` is a FORMULA method, not a Cask
  # one. `brew install --cask` rejects the whole definition with
  # "undefined method 'license' for Cask". The license is MIT; see README.

  on_arm do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-arm64.tar.gz"
    sha256 "c8e1a38964445a093195fad93699b71e1dc0e953844fbd3b78a2e0613bc4609e"
  end

  on_intel do
    url "https://github.com/sky-jiangcheng/cinder/releases/download/v#{version}/cinder-mcp-#{version}-darwin-amd64.tar.gz"
    sha256 "bf01966b079e8ba7e73ed8bec2744b3edd8ed59ecce0f8435bd54d597b3a98e9"
  end

  # The tarball stages `cinder-mcp` plus a `repotory-mcp` symlink; `binary`
  # links both into $(brew --prefix)/bin. The old name is the one every
  # existing client config already points at, so it has to keep resolving.
  binary "cinder-mcp"
  binary "cinder-mcp", target: "repotory-mcp"
end
