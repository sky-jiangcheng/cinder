package version

// CoreVersion is the version of the memory-layer contract — the outward face
// that agents and IDE plugins program against: the MCP tool set, the HTTP
// /api/rpc surface and the SQLite schema.
//
// It moves on its own schedule, not with the release version. Version.Version
// answers "which build is this" (the desktop app ships UI changes every few
// days); CoreVersion answers "what may a client rely on" (the contract
// changes rarely). Conflating the two meant a client had to guess whether a
// version bump was cosmetic or breaking.
//
//	minor — additive: a new tool, a new optional field, a new endpoint
//	patch — no contract change at all
//	major — breaking: a tool removed or renamed, a field's meaning changed
//
// The value is pinned next to the tool list in cmd/mcp/main_test.go, and
// scripts/check-version-drift.mjs fails if the two drift apart.
const CoreVersion = "2.3.0"

// CoreVersionPrevious / CoreVersionChange make the bump itself checkable.
// The old gate only compared two copies of the number, so "deleted a tool but
// only bumped minor" passed. scripts/check-version-drift.mjs now validates
// that the declared change kind matches the actual distance between the two
// versions. The previous bump was a minor (ADR-0063: the new
// repotory_config_get / repotory_config_update tools are additive, and the
// config-write reclassification changes which process executes the write, not
// the existing tool surface); the current one is a minor too: repotory_chat
// gains an optional set_path operation (re-attach a thread to a repository,
// or clear it back to unscoped) — additive, and every existing operation's
// shape stays untouched.
const CoreVersionPrevious = "2.2.0"
const CoreVersionChange = "minor" // major | minor | patch
